package rhel82

import (
	"errors"
	"log"
	"sort"
	"strconv"

	"github.com/google/uuid"

	"github.com/osbuild/osbuild-composer/internal/blueprint"
	"github.com/osbuild/osbuild-composer/internal/crypt"
	"github.com/osbuild/osbuild-composer/internal/osbuild"
	"github.com/osbuild/osbuild-composer/internal/rpmmd"
)

type RHEL82 struct {
	arches        map[string]arch
	outputs       map[string]output
	buildPackages []string
}

type arch struct {
	Name               string
	BootloaderPackages []string
	BuildPackages      []string
	UEFI               bool
	Repositories       []rpmmd.RepoConfig
}

type output struct {
	Name             string
	MimeType         string
	Packages         []string
	ExcludedPackages []string
	EnabledServices  []string
	DisabledServices []string
	Bootable         bool
	DefaultTarget    string
	KernelOptions    string
	DefaultSize      uint64
	Assembler        func(uefi bool, size uint64) *osbuild.Assembler
}

const Name = "rhel-8.2"
const ModulePlatformID = "platform:el8"

func New(confPaths []string) *RHEL82 {
	const GigaByte = 1024 * 1024 * 1024

	r := RHEL82{
		arches:  map[string]arch{},
		outputs: map[string]output{},
		buildPackages: []string{
			"dnf",
			"dosfstools",
			"dracut-config-generic",
			"e2fsprogs",
			"glibc",
			"policycoreutils",
			"python36",
			"qemu-img",
			"systemd",
			"tar",
			"xfsprogs",
		},
	}

	repoMap, err := rpmmd.LoadRepositories(confPaths, Name)
	if err != nil {
		log.Printf("Could not load repository data for %s: %s", Name, err.Error())
		return nil
	}

	repos, exists := repoMap["x86_64"]
	if !exists {
		log.Printf("Could not load architecture-specific repository data for x86_64 (%s): %s", Name, err.Error())
	} else {
		r.arches["x86_64"] = arch{
			Name: "x86_64",
			BootloaderPackages: []string{
				"grub2-pc",
			},
			BuildPackages: []string{
				"grub2-pc",
			},
			Repositories: repos,
		}
	}

	repos, exists = repoMap["aarch64"]
	if !exists {
		log.Printf("Could not load architecture-specific repository data for aarch64 (%s): %s", Name, err.Error())
	} else {
		r.arches["aarch64"] = arch{
			Name: "aarch64",
			BootloaderPackages: []string{
				"dracut-config-generic",
				"efibootmgr",
				"grub2-efi-aa64",
				"grub2-tools",
				"shim-aa64",
			},
			UEFI:         true,
			Repositories: repos,
		}
	}

	r.outputs["ami"] = output{
		Name:     "image.raw.xz",
		MimeType: "application/octet-stream",
		Packages: []string{
			"checkpolicy",
			"chrony",
			"cloud-init",
			"cloud-init",
			"cloud-utils-growpart",
			"@core",
			"dhcp-client",
			"dracut-config-generic",
			"gdisk",
			"insights-client",
			"kernel",
			"langpacks-en",
			"net-tools",
			"NetworkManager",
			"redhat-release",
			"redhat-release-eula",
			"rng-tools",
			"rsync",
			"selinux-policy-targeted",
			"tar",
			"yum-utils",

			// TODO this doesn't exist in BaseOS or AppStream
			// "rh-amazon-rhui-client",
		},
		ExcludedPackages: []string{
			"aic94xx-firmware",
			"alsa-firmware",
			"alsa-lib",
			"alsa-tools-firmware",
			"biosdevname",
			"dracut-config-rescue",
			"firewalld",
			"iprutils",
			"ivtv-firmware",
			"iwl1000-firmware",
			"iwl100-firmware",
			"iwl105-firmware",
			"iwl135-firmware",
			"iwl2000-firmware",
			"iwl2030-firmware",
			"iwl3160-firmware",
			"iwl3945-firmware",
			"iwl4965-firmware",
			"iwl5000-firmware",
			"iwl5150-firmware",
			"iwl6000-firmware",
			"iwl6000g2a-firmware",
			"iwl6000g2b-firmware",
			"iwl6050-firmware",
			"iwl7260-firmware",
			"libertas-sd8686-firmware",
			"libertas-sd8787-firmware",
			"libertas-usb8388-firmware",
			"plymouth",

			// TODO this cannot be removed, because the kernel (?)
			// depends on it. The ec2 kickstart force-removes it.
			// "linux-firmware",

			// TODO setfiles failes because of usr/sbin/timedatex. Exlude until
			// https://errata.devel.redhat.com/advisory/47339 lands
			"timedatex",
		},
		DefaultTarget: "multi-user.target",
		Bootable:      true,
		KernelOptions: "ro console=ttyS0,115200n8 console=tty0 net.ifnames=0 rd.blacklist=nouveau nvme_core.io_timeout=4294967295 crashkernel=auto",
		DefaultSize:   6 * GigaByte,
		Assembler: func(uefi bool, size uint64) *osbuild.Assembler {
			return r.qemuAssembler("raw.xz", "image.raw.xz", uefi, size)
		},
	}

	r.outputs["ext4-filesystem"] = output{
		Name:     "filesystem.img",
		MimeType: "application/octet-stream",
		Packages: []string{
			"policycoreutils",
			"selinux-policy-targeted",
			"kernel",
			"firewalld",
			"chrony",
			"dracut-config-generic",
			"langpacks-en",
		},
		ExcludedPackages: []string{
			"dracut-config-rescue",

			// TODO setfiles failes because of usr/sbin/timedatex. Exlude until
			// https://errata.devel.redhat.com/advisory/47339 lands
			"timedatex",
		},
		Bootable:      false,
		KernelOptions: "ro net.ifnames=0",
		DefaultSize:   2 * GigaByte,
		Assembler:     func(uefi bool, size uint64) *osbuild.Assembler { return r.rawFSAssembler("filesystem.img", size) },
	}

	r.outputs["partitioned-disk"] = output{
		Name:     "disk.img",
		MimeType: "application/octet-stream",
		Packages: []string{
			"@core",
			"chrony",
			"dracut-config-generic",
			"firewalld",
			"kernel",
			"langpacks-en",
			"selinux-policy-targeted",
		},
		ExcludedPackages: []string{
			"dracut-config-rescue",

			// TODO setfiles failes because of usr/sbin/timedatex. Exlude until
			// https://errata.devel.redhat.com/advisory/47339 lands
			"timedatex",
		},
		Bootable:      true,
		KernelOptions: "ro net.ifnames=0",
		DefaultSize:   2 * GigaByte,
		Assembler: func(uefi bool, size uint64) *osbuild.Assembler {
			return r.qemuAssembler("raw", "disk.img", uefi, size)
		},
	}

	r.outputs["qcow2"] = output{
		Name:     "disk.qcow2",
		MimeType: "application/x-qemu-disk",
		Packages: []string{
			"@core",
			"chrony",
			"dnf",
			"kernel",
			"yum",
			"nfs-utils",
			"dnf-utils",
			"cloud-init",
			"python3-jsonschema",
			"qemu-guest-agent",
			"cloud-utils-growpart",
			"dracut-config-generic",
			"dracut-norescue",
			"tar",
			"tcpdump",
			"rsync",
			"dnf-plugin-spacewalk",
			"rhn-client-tools",
			"rhnlib",
			"rhnsd",
			"rhn-setup",
			"NetworkManager",
			"dhcp-client",
			"cockpit-ws",
			"cockpit-system",
			"subscription-manager-cockpit",
			"redhat-release",
			"redhat-release-eula",
			"rng-tools",
			"insights-client",
			// TODO: rh-amazon-rhui-client
		},
		ExcludedPackages: []string{
			"dracut-config-rescue",
			"aic94xx-firmware",
			"alsa-firmware",
			"alsa-lib",
			"alsa-tools-firmware",
			"firewalld",
			"ivtv-firmware",
			"iwl1000-firmware",
			"iwl100-firmware",
			"iwl105-firmware",
			"iwl135-firmware",
			"iwl2000-firmware",
			"iwl2030-firmware",
			"iwl3160-firmware",
			"iwl3945-firmware",
			"iwl4965-firmware",
			"iwl5000-firmware",
			"iwl5150-firmware",
			"iwl6000-firmware",
			"iwl6000g2a-firmware",
			"iwl6000g2b-firmware",
			"iwl6050-firmware",
			"iwl7260-firmware",
			"libertas-sd8686-firmware",
			"libertas-sd8787-firmware",
			"libertas-usb8388-firmware",
			"langpacks-*",
			"langpacks-en",
			"biosdevname",
			"plymouth",
			"iprutils",
			"langpacks-en",
			"fedora-release",
			"fedora-repos",

			// TODO setfiles failes because of usr/sbin/timedatex. Exlude until
			// https://errata.devel.redhat.com/advisory/47339 lands
			"timedatex",
		},
		Bootable:      true,
		KernelOptions: "console=ttyS0 console=ttyS0,115200n8 no_timer_check crashkernel=auto net.ifnames=0",
		DefaultSize:   2 * GigaByte,
		Assembler: func(uefi bool, size uint64) *osbuild.Assembler {
			return r.qemuAssembler("qcow2", "disk.qcow2", uefi, size)
		},
	}

	r.outputs["openstack"] = output{
		Name:     "disk.qcow2",
		MimeType: "application/x-qemu-disk",
		Packages: []string{
			// Defaults
			"@Core",
			"langpacks-en",

			// Don't run dracut in host-only mode, in order to pull in
			// the hv_vmbus, hv_netvsc and hv_storvsc modules into the initrd.
			"dracut-config-generic",

			// From the lorax kickstart
			"kernel",
			"selinux-policy-targeted",
			"cloud-init",
			"qemu-guest-agent",
			"spice-vdagent",
		},
		ExcludedPackages: []string{
			"dracut-config-rescue",
		},
		Bootable:      true,
		KernelOptions: "ro net.ifnames=0",
		DefaultSize:   2 * GigaByte,
		Assembler: func(uefi bool, size uint64) *osbuild.Assembler {
			return r.qemuAssembler("qcow2", "disk.qcow2", uefi, size)
		},
	}

	r.outputs["tar"] = output{
		Name:     "root.tar.xz",
		MimeType: "application/x-tar",
		Packages: []string{
			"policycoreutils",
			"selinux-policy-targeted",
			"kernel",
			"firewalld",
			"chrony",
			"dracut-config-generic",
			"langpacks-en",
		},
		ExcludedPackages: []string{
			"dracut-config-rescue",

			// TODO setfiles failes because of usr/sbin/timedatex. Exlude until
			// https://errata.devel.redhat.com/advisory/47339 lands
			"timedatex",
		},
		Bootable:      false,
		KernelOptions: "ro net.ifnames=0",
		Assembler:     func(uefi bool, size uint64) *osbuild.Assembler { return r.tarAssembler("root.tar.xz", "xz") },
	}

	r.outputs["vhd"] = output{
		Name:     "disk.vhd",
		MimeType: "application/x-vhd",
		Packages: []string{
			// Defaults
			"@Core",
			"langpacks-en",

			// Don't run dracut in host-only mode, in order to pull in
			// the hv_vmbus, hv_netvsc and hv_storvsc modules into the initrd.
			"dracut-config-generic",

			// From the lorax kickstart
			"kernel",
			"selinux-policy-targeted",
			"chrony",
			"WALinuxAgent",
			"python3",
			"net-tools",
			"cloud-init",
			"cloud-utils-growpart",
			"gdisk",
		},
		ExcludedPackages: []string{
			"dracut-config-rescue",

			// TODO setfiles failes because of usr/sbin/timedatex. Exlude until
			// https://errata.devel.redhat.com/advisory/47339 lands
			"timedatex",
		},
		EnabledServices: []string{
			"sshd",
			"waagent",
		},
		DefaultTarget: "multi-user.target",
		Bootable:      true,
		KernelOptions: "ro biosdevname=0 rootdelay=300 console=ttyS0 earlyprintk=ttyS0 net.ifnames=0",
		DefaultSize:   2 * GigaByte,
		Assembler: func(uefi bool, size uint64) *osbuild.Assembler {
			return r.qemuAssembler("vpc", "disk.vhd", uefi, size)
		},
	}

	r.outputs["vmdk"] = output{
		Name:     "disk.vmdk",
		MimeType: "application/x-vmdk",
		Packages: []string{
			"@core",
			"chrony",
			"dracut-config-generic",
			"firewalld",
			"kernel",
			"langpacks-en",
			"open-vm-tools",
			"selinux-policy-targeted",
		},
		ExcludedPackages: []string{
			"dracut-config-rescue",

			// TODO setfiles failes because of usr/sbin/timedatex. Exlude until
			// https://errata.devel.redhat.com/advisory/47339 lands
			"timedatex",
		},
		Bootable:      true,
		KernelOptions: "ro net.ifnames=0",
		DefaultSize:   2 * GigaByte,
		Assembler: func(uefi bool, size uint64) *osbuild.Assembler {
			return r.qemuAssembler("vmdk", "disk.vmdk", uefi, size)
		},
	}

	return &r
}

func (r *RHEL82) Name() string {
	return Name
}

func (r *RHEL82) ModulePlatformID() string {
	return ModulePlatformID
}

func (r *RHEL82) Repositories(arch string) []rpmmd.RepoConfig {
	return r.arches[arch].Repositories
}

func (r *RHEL82) ListOutputFormats() []string {
	formats := make([]string, 0, len(r.outputs))
	for name := range r.outputs {
		formats = append(formats, name)
	}
	sort.Strings(formats)
	return formats
}

func (r *RHEL82) FilenameFromType(outputFormat string) (string, string, error) {
	if output, exists := r.outputs[outputFormat]; exists {
		return output.Name, output.MimeType, nil
	}
	return "", "", errors.New("invalid output format: " + outputFormat)
}

func (r *RHEL82) GetSizeForOutputType(outputFormat string, size uint64) uint64 {
	const MegaByte = 1024 * 1024
	// Microsoft Azure requires vhd images to be rounded up to the nearest MB
	if outputFormat == "vhd" && size%MegaByte != 0 {
		size = (size/MegaByte + 1) * MegaByte
	}
	if size == 0 {
		size = r.outputs[outputFormat].DefaultSize
	}
	return size
}

func (r *RHEL82) BasePackages(outputFormat string, outputArchitecture string) ([]string, []string, error) {
	output, exists := r.outputs[outputFormat]
	if !exists {
		return nil, nil, errors.New("invalid output format: " + outputFormat)
	}

	packages := output.Packages
	if output.Bootable {
		arch, exists := r.arches[outputArchitecture]
		if !exists {
			return nil, nil, errors.New("invalid architecture: " + outputArchitecture)
		}

		packages = append(packages, arch.BootloaderPackages...)
	}

	return packages, output.ExcludedPackages, nil
}

func (r *RHEL82) BuildPackages(outputArchitecture string) ([]string, error) {
	arch, exists := r.arches[outputArchitecture]
	if !exists {
		return nil, errors.New("invalid architecture: " + outputArchitecture)
	}

	return append(r.buildPackages, arch.BuildPackages...), nil
}

func (r *RHEL82) Pipeline(b *blueprint.Blueprint, additionalRepos []rpmmd.RepoConfig, packageSpecs, buildPackageSpecs []rpmmd.PackageSpec, checksums map[string]string, outputArchitecture, outputFormat string, size uint64) (*osbuild.Pipeline, error) {
	output, exists := r.outputs[outputFormat]
	if !exists {
		return nil, errors.New("invalid output format: " + outputFormat)
	}

	arch, exists := r.arches[outputArchitecture]
	if !exists {
		return nil, errors.New("invalid architecture: " + outputArchitecture)
	}

	p := &osbuild.Pipeline{}
	p.SetBuild(r.buildPipeline(arch, checksums), "org.osbuild.rhel82")

	packages, excludedPackages, err := r.BasePackages(outputFormat, outputArchitecture)
	if err != nil {
		return nil, err
	}
	packages = append(packages, b.GetPackages()...)
	p.AddStage(osbuild.NewDNFStage(r.dnfStageOptions(arch, additionalRepos, checksums, packages, excludedPackages)))
	p.AddStage(osbuild.NewFixBLSStage())

	if output.Bootable {
		p.AddStage(osbuild.NewFSTabStage(r.fsTabStageOptions(arch.UEFI)))
	}

	kernelOptions := output.KernelOptions
	if kernel := b.GetKernel(); kernel != nil {
		kernelOptions += " " + kernel.Append
	}
	p.AddStage(osbuild.NewGRUB2Stage(r.grub2StageOptions(kernelOptions, arch.UEFI)))

	// TODO support setting all languages and install corresponding langpack-* package
	language, keyboard := b.GetPrimaryLocale()

	if language != nil {
		p.AddStage(osbuild.NewLocaleStage(&osbuild.LocaleStageOptions{*language}))
	} else {
		p.AddStage(osbuild.NewLocaleStage(&osbuild.LocaleStageOptions{"en_US"}))
	}

	if keyboard != nil {
		p.AddStage(osbuild.NewKeymapStage(&osbuild.KeymapStageOptions{*keyboard}))
	}

	if hostname := b.GetHostname(); hostname != nil {
		p.AddStage(osbuild.NewHostnameStage(&osbuild.HostnameStageOptions{*hostname}))
	}

	timezone, ntpServers := b.GetTimezoneSettings()

	// TODO install chrony when this is set?
	if timezone != nil {
		p.AddStage(osbuild.NewTimezoneStage(&osbuild.TimezoneStageOptions{*timezone}))
	}

	if len(ntpServers) > 0 {
		p.AddStage(osbuild.NewChronyStage(&osbuild.ChronyStageOptions{ntpServers}))
	}

	if users := b.GetUsers(); len(users) > 0 {
		options, err := r.userStageOptions(users)
		if err != nil {
			return nil, err
		}
		p.AddStage(osbuild.NewUsersStage(options))
	}

	if groups := b.GetGroups(); len(groups) > 0 {
		p.AddStage(osbuild.NewGroupsStage(r.groupStageOptions(groups)))
	}

	if services := b.GetServices(); services != nil || output.EnabledServices != nil {
		p.AddStage(osbuild.NewSystemdStage(r.systemdStageOptions(output.EnabledServices, output.DisabledServices, services, output.DefaultTarget)))
	}

	if firewall := b.GetFirewall(); firewall != nil {
		p.AddStage(osbuild.NewFirewallStage(r.firewallStageOptions(firewall)))
	}

	p.AddStage(osbuild.NewSELinuxStage(r.selinuxStageOptions()))

	p.Assembler = output.Assembler(arch.UEFI, size)

	return p, nil
}

func (r *RHEL82) Sources(packages []rpmmd.PackageSpec) *osbuild.Sources {
	return &osbuild.Sources{}
}

func (r *RHEL82) Runner() string {
	return "org.osbuild.rhel82"
}

func (r *RHEL82) buildPipeline(arch arch, checksums map[string]string) *osbuild.Pipeline {
	packages, err := r.BuildPackages(arch.Name)
	if err != nil {
		panic("impossibly invalid arch")
	}

	p := &osbuild.Pipeline{}
	p.AddStage(osbuild.NewDNFStage(r.dnfStageOptions(arch, nil, checksums, packages, nil)))
	return p
}

func (r *RHEL82) dnfStageOptions(arch arch, additionalRepos []rpmmd.RepoConfig, checksums map[string]string, packages, excludedPackages []string) *osbuild.DNFStageOptions {
	options := &osbuild.DNFStageOptions{
		ReleaseVersion:   "8",
		BaseArchitecture: arch.Name,
		ModulePlatformId: ModulePlatformID,
	}
	for _, repo := range append(arch.Repositories, additionalRepos...) {
		options.AddRepository(&osbuild.DNFRepository{
			BaseURL:    repo.BaseURL,
			MetaLink:   repo.Metalink,
			MirrorList: repo.MirrorList,
			Checksum:   checksums[repo.Id],
		})
	}

	sort.Strings(packages)
	for _, pkg := range packages {
		options.AddPackage(pkg)
	}

	sort.Strings(excludedPackages)
	for _, pkg := range excludedPackages {
		options.ExcludePackage(pkg)
	}

	return options
}

func (r *RHEL82) userStageOptions(users []blueprint.UserCustomization) (*osbuild.UsersStageOptions, error) {
	options := osbuild.UsersStageOptions{
		Users: make(map[string]osbuild.UsersStageOptionsUser),
	}

	for _, c := range users {
		if c.Password != nil && !crypt.PasswordIsCrypted(*c.Password) {
			cryptedPassword, err := crypt.CryptSHA512(*c.Password)
			if err != nil {
				return nil, err
			}

			c.Password = &cryptedPassword
		}

		user := osbuild.UsersStageOptionsUser{
			Groups:      c.Groups,
			Description: c.Description,
			Home:        c.Home,
			Shell:       c.Shell,
			Password:    c.Password,
			Key:         c.Key,
		}

		if c.UID != nil {
			uid := strconv.Itoa(*c.UID)
			user.UID = &uid
		}

		if c.GID != nil {
			gid := strconv.Itoa(*c.GID)
			user.GID = &gid
		}

		options.Users[c.Name] = user
	}

	return &options, nil
}

func (r *RHEL82) groupStageOptions(groups []blueprint.GroupCustomization) *osbuild.GroupsStageOptions {
	options := osbuild.GroupsStageOptions{
		Groups: map[string]osbuild.GroupsStageOptionsGroup{},
	}

	for _, group := range groups {
		groupData := osbuild.GroupsStageOptionsGroup{
			Name: group.Name,
		}
		if group.GID != nil {
			gid := strconv.Itoa(*group.GID)
			groupData.GID = &gid
		}

		options.Groups[group.Name] = groupData
	}

	return &options
}

func (r *RHEL82) firewallStageOptions(firewall *blueprint.FirewallCustomization) *osbuild.FirewallStageOptions {
	options := osbuild.FirewallStageOptions{
		Ports: firewall.Ports,
	}

	if firewall.Services != nil {
		options.EnabledServices = firewall.Services.Enabled
		options.DisabledServices = firewall.Services.Disabled
	}

	return &options
}

func (r *RHEL82) systemdStageOptions(enabledServices, disabledServices []string, s *blueprint.ServicesCustomization, target string) *osbuild.SystemdStageOptions {
	if s != nil {
		enabledServices = append(enabledServices, s.Enabled...)
		enabledServices = append(disabledServices, s.Disabled...)
	}
	return &osbuild.SystemdStageOptions{
		EnabledServices:  enabledServices,
		DisabledServices: disabledServices,
		DefaultTarget:    target,
	}
}

func (r *RHEL82) fsTabStageOptions(uefi bool) *osbuild.FSTabStageOptions {
	options := osbuild.FSTabStageOptions{}
	options.AddFilesystem("0bd700f8-090f-4556-b797-b340297ea1bd", "xfs", "/", "defaults", 0, 0)
	if uefi {
		options.AddFilesystem("46BB-8120", "vfat", "/boot/efi", "umask=0077,shortname=winnt", 0, 2)
	}
	return &options
}

func (r *RHEL82) grub2StageOptions(kernelOptions string, uefi bool) *osbuild.GRUB2StageOptions {
	id, err := uuid.Parse("0bd700f8-090f-4556-b797-b340297ea1bd")
	if err != nil {
		panic("invalid UUID")
	}

	var uefiOptions *osbuild.GRUB2UEFI
	if uefi {
		uefiOptions = &osbuild.GRUB2UEFI{
			Vendor: "redhat",
		}
	}

	return &osbuild.GRUB2StageOptions{
		RootFilesystemUUID: id,
		KernelOptions:      kernelOptions,
		Legacy:             !uefi,
		UEFI:               uefiOptions,
	}
}

func (r *RHEL82) selinuxStageOptions() *osbuild.SELinuxStageOptions {
	return &osbuild.SELinuxStageOptions{
		FileContexts: "etc/selinux/targeted/contexts/files/file_contexts",
	}
}

func (r *RHEL82) qemuAssembler(format string, filename string, uefi bool, size uint64) *osbuild.Assembler {
	var options osbuild.QEMUAssemblerOptions
	if uefi {
		fstype := uuid.MustParse("C12A7328-F81F-11D2-BA4B-00A0C93EC93B")
		options = osbuild.QEMUAssemblerOptions{
			Format:   format,
			Filename: filename,
			Size:     size,
			PTUUID:   "8DFDFF87-C96E-EA48-A3A6-9408F1F6B1EF",
			PTType:   "gpt",
			Partitions: []osbuild.QEMUPartition{
				{
					Start: 2048,
					Size:  972800,
					Type:  &fstype,
					Filesystem: osbuild.QEMUFilesystem{
						Type:       "vfat",
						UUID:       "46BB-8120",
						Label:      "EFI System Partition",
						Mountpoint: "/boot/efi",
					},
				},
				{
					Start: 976896,
					Filesystem: osbuild.QEMUFilesystem{
						Type:       "xfs",
						UUID:       "0bd700f8-090f-4556-b797-b340297ea1bd",
						Mountpoint: "/",
					},
				},
			},
		}
	} else {
		options = osbuild.QEMUAssemblerOptions{
			Format:   format,
			Filename: filename,
			Size:     size,
			PTUUID:   "0x14fc63d2",
			PTType:   "mbr",
			Partitions: []osbuild.QEMUPartition{
				{
					Start:    2048,
					Bootable: true,
					Filesystem: osbuild.QEMUFilesystem{
						Type:       "xfs",
						UUID:       "0bd700f8-090f-4556-b797-b340297ea1bd",
						Mountpoint: "/",
					},
				},
			},
		}
	}
	return osbuild.NewQEMUAssembler(&options)
}

func (r *RHEL82) tarAssembler(filename, compression string) *osbuild.Assembler {
	return osbuild.NewTarAssembler(
		&osbuild.TarAssemblerOptions{
			Filename:    filename,
			Compression: compression,
		})
}

func (r *RHEL82) rawFSAssembler(filename string, size uint64) *osbuild.Assembler {
	id, err := uuid.Parse("0bd700f8-090f-4556-b797-b340297ea1bd")
	if err != nil {
		panic("invalid UUID")
	}
	return osbuild.NewRawFSAssembler(
		&osbuild.RawFSAssemblerOptions{
			Filename:           filename,
			RootFilesystemUUDI: id,
			Size:               size,
			FilesystemType:     "xfs",
		})
}
