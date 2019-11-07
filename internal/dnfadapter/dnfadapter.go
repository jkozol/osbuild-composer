package dnfadapter

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"time"
)

type RepoConfig struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	BaseURL    string `json:"baseurl,omitempty"`
	Metalink   string `json:"metalink,omitempty"`
	MirrorList string `json:"mirrorlist,omitempty"`
}

type PackageList []Package

type Package struct {
	Name        string
	Summary     string
	Description string
	URL         string
	Epoch       uint
	Version     string
	Release     string
	Arch        string
	BuildTime   time.Time
	License     string
}

type PackageSpec struct {
	Name    string `json:"name"`
	Epoch   uint   `json:"epoch"`
	Version string `json:"version,omitempty"`
	Release string `json:"release,omitempty"`
	Arch    string `json:"arch,omitempty"`
}

type DNFAdapter struct {
	DNFJsonPath string
	ExtraArgs   []string
}

type DNFError struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

func (err *DNFError) Error() string {
	return fmt.Sprintf("DNF error occured: %s: %s", err.Kind, err.Reason)
}

func (d *DNFAdapter) runDNF(command string, arguments interface{}, result interface{}) error {
	var call = struct {
		Command   string      `json:"command"`
		Arguments interface{} `json:"arguments,omitempty"`
	}{
		command,
		arguments,
	}

	args := append([]string{d.DNFJsonPath}, d.ExtraArgs...)

	cmd := exec.Command("python3", args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	err = json.NewEncoder(stdin).Encode(call)
	if err != nil {
		return err
	}
	stdin.Close()

	err = json.NewDecoder(stdout).Decode(result)
	if err != nil {
		return err
	}

	err = cmd.Wait()

	const DnfErrorExitCode = 10
	if runError, ok := err.(*exec.ExitError); ok && runError.ExitCode() == DnfErrorExitCode {
		dnfError := new(DNFError)
		err = json.Unmarshal(runError.Stderr, dnfError)

		return dnfError
	}
	return err
}

func (d *DNFAdapter) FetchPackageList(repos []RepoConfig) (PackageList, error) {
	var arguments = struct {
		Repos []RepoConfig `json:"repos"`
	}{repos}
	var packages PackageList
	err := d.runDNF("dump", arguments, &packages)
	return packages, err
}

func (d *DNFAdapter) Depsolve(specs []string, repos []RepoConfig) ([]PackageSpec, error) {
	var arguments = struct {
		PackageSpecs []string     `json:"package-specs"`
		Repos        []RepoConfig `json:"repos"`
	}{specs, repos}
	var dependencies []PackageSpec
	err := d.runDNF("depsolve", arguments, &dependencies)
	return dependencies, err
}

func (packages PackageList) Search(name string) (int, int) {
	first := sort.Search(len(packages), func(i int) bool {
		return packages[i].Name >= name
	})

	if first == len(packages) || packages[first].Name != name {
		return first, 0
	}

	last := first + 1
	for last < len(packages) && packages[last].Name == name {
		last++
	}

	return first, last - first
}
