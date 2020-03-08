package distro_test

import (
	"encoding/json"
	"io/ioutil"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/osbuild/osbuild-composer/internal/blueprint"
	"github.com/osbuild/osbuild-composer/internal/distro"
	"github.com/osbuild/osbuild-composer/internal/osbuild"
)

func TestDistro_Pipeline(t *testing.T) {
	pipelinePath := "../../test/cases/"
	fileInfos, err := ioutil.ReadDir(pipelinePath)
	if err != nil {
		t.Errorf("Could not read pipelines directory '%s': %v", pipelinePath, err)
	}
	for _, fileInfo := range fileInfos {
		type compose struct {
			Distro       string               `json:"distro"`
			Arch         string               `json:"arch"`
			OutputFormat string               `json:"output-format"`
			Blueprint    *blueprint.Blueprint `json:"blueprint"`
		}
		type rpmMD struct {
			Checksums map[string]string `json:"checksums"`
		}
		var tt struct {
			Compose  *compose          `json:"compose"`
			RpmMD    *rpmMD            `json:"rpmmd"`
			Pipeline *osbuild.Pipeline `json:"pipeline,omitempty"`
		}
		file, err := ioutil.ReadFile(pipelinePath + fileInfo.Name())
		if err != nil {
			t.Errorf("Colud not read test-case '%s': %v", fileInfo.Name(), err)
		}
		err = json.Unmarshal([]byte(file), &tt)
		if err != nil {
			t.Errorf("Colud not parse test-case '%s': %v", fileInfo.Name(), err)
		}
		if tt.Compose == nil || tt.Compose.Blueprint == nil {
			t.Logf("Skipping '%s'.", fileInfo.Name())
			continue
		}
		t.Run(tt.Compose.OutputFormat, func(t *testing.T) {
			distros, err := distro.NewDefaultRegistry([]string{"../.."})
			if err != nil {
				t.Fatal(err)
			}
			d := distros.GetDistro(tt.Compose.Distro)
			if d == nil {
				t.Errorf("unknown distro: %v", tt.Compose.Distro)
				return
			}
			size := d.GetSizeForOutputType(tt.Compose.OutputFormat, 0)
			got, err := d.Pipeline(tt.Compose.Blueprint, nil, nil, nil, tt.RpmMD.Checksums, tt.Compose.Arch, tt.Compose.OutputFormat, size)
			if (err != nil) != (tt.Pipeline == nil) {
				t.Errorf("distro.Pipeline() error = %v", err)
				return
			}
			if tt.Pipeline != nil {
				if !reflect.DeepEqual(got, tt.Pipeline) {
					// Without this the "difference" is just a list of pointers.
					gotJson, _ := json.Marshal(got)
					fileJson, _ := json.Marshal(tt.Pipeline)
					t.Errorf("d.Pipeline() =\n%v,\nwant =\n%v", string(gotJson), string(fileJson))
				}
			}
		})
	}
}

// Test that all distros are registered properly and that Registry.List() works.
func TestDistro_RegistryList(t *testing.T) {
	expected := []string{
		"fedora-30",
		"fedora-31",
		"fedora-32",
		"rhel-8.1",
		"rhel-8.2",
	}

	distros, err := distro.NewDefaultRegistry([]string{"../.."})
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(distros.List(), expected); diff != "" {
		t.Errorf("unexpected list of distros: %v", diff)
	}
}
