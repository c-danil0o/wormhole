package backends

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/c12s/wormhole/internal/config"
)

// LibvirtBackend provisions bootc node VMs on the local KVM host
// (decisions D7.2 / D7.1, the E2 experiment substrate):
//
//   each VM is a qcow2 overlay over a Supernova-produced bootc OS image,
//   plus a NoCloud seed that injects /var/lib/c12s/node.env on first boot.
//   star runs inside the VM as a systemd service and enrolls into the CP
//   like any other node — provisioning stays out of band, exactly as the
//   thesis boundary requires (D1).

const (
	libvirtURI        = "qemu:///system"
	libvirtNamePrefix = "c12s-"
	libvirtDiskSizeGB = 20
)

type LibvirtBackend struct {
	conf *config.Config
}

func NewLibvirtBackend(conf *config.Config) (*LibvirtBackend, error) {
	return &LibvirtBackend{conf: conf}, nil
}

func (l *LibvirtBackend) Setup(conf *config.Config) error {
	l.conf = conf
	if _, err := os.Stat(conf.DiskImage); err != nil {
		return fmt.Errorf("source disk image: %w", err)
	}
	for _, bin := range []string{"virt-install", "virsh", "xorriso"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found in PATH", bin)
		}
	}
	if err := os.MkdirAll(l.stateDir(), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	return nil
}

func (l *LibvirtBackend) stateDir() string {
	if l.conf.StateDir != "" {
		return expandHome(l.conf.StateDir)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".wormhole", "libvirt")
}

func (l *LibvirtBackend) domain(name string) string {
	return libvirtNamePrefix + name
}

func (l *LibvirtBackend) vmDir(name string) string {
	return filepath.Join(l.stateDir(), name)
}

func (l *LibvirtBackend) Create(vms ...string) error {
	for _, vm := range vms {
		if err := l.createVM(vm); err != nil {
			return fmt.Errorf("create %s: %w", vm, err)
		}
	}
	return nil
}

func (l *LibvirtBackend) createVM(name string) error {
	dir := l.vmDir(name)
	disk := filepath.Join(dir, "disk.qcow2")
	seed := filepath.Join(dir, "seed.iso")
	domain := l.domain(name)

	if l.domainExists(domain) {
		return fmt.Errorf("domain %s already exists (destroy it first)", domain)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	if err := buildSeed(dir, domain, l.conf.NATSAddress, l.conf.Extras); err != nil {
		return err
	}
	if err := createDiskOverlay(l.conf.DiskImage, disk); err != nil {
		return err
	}

	fmt.Printf("==> creating VM %s (domain %s)\n", name, domain)
	return l.virshInstall(
		"--name", domain,
		"--memory", fmt.Sprintf("%d", l.conf.Memory),
		"--vcpus", fmt.Sprintf("%d", l.conf.CPUs),
		"--disk", "path="+disk+",format=qcow2",
		"--disk", "path="+seed+",device=cdrom",
		"--network", "network="+l.networkName()+",model=virtio",
		"--os-variant", l.osVariant(),
		graphicsFlag(l.conf.GUI),
	)
}

func (l *LibvirtBackend) Start(vms ...string) error {
	return l.forEach(vms, "start", nil)
}

// Resume is the same as Start for libvirt: a stopped VM boots from disk.
func (l *LibvirtBackend) Resume(vms ...string) error {
	return l.forEach(vms, "start", nil)
}

// Reload reboots a running VM (ACPI request to the guest).
func (l *LibvirtBackend) Reload(vms ...string) error {
	return l.forEach(vms, "reboot", nil)
}

// Stop = graceful ACPI shutdown, waits for the guest.
func (l *LibvirtBackend) Stop(vms ...string) error {
	return l.forEach(vms, "shutdown", nil)
}

// ShutDown = hard poweroff.
func (l *LibvirtBackend) ShutDown(vms ...string) error {
	return l.forEach(vms, "destroy", nil)
}

// StartNodes: on a bootc VM the node agent *is* the boot (star.service),
// so starting the node = starting the VM.
func (l *LibvirtBackend) StartNodes(vms ...string) error {
	return l.forEach(vms, "start", nil)
}

func (l *LibvirtBackend) StopNodes(vms ...string) error {
	return l.forEach(vms, "shutdown", nil)
}

func (l *LibvirtBackend) Destroy(vms ...string) error {
	for _, vm := range vms {
		domain := l.domain(vm)
		fmt.Printf("==> destroying %s\n", domain)
		// destroy (poweroff) first so undefine always works
		_ = l.virsh("destroy", domain)
		if err := l.virsh("undefine", domain, "--nvram", "--remove-all-storage"); err != nil {
			// remove-all-storage would delete the overlay disk; seed stays
			// in the state dir and is cleaned below anyway
			if err2 := l.virsh("undefine", domain, "--nvram"); err2 != nil {
				return fmt.Errorf("undefine %s: %w", domain, err)
			}
		}
		if err := os.RemoveAll(l.vmDir(vm)); err != nil {
			return fmt.Errorf("remove state for %s: %w", vm, err)
		}
	}
	return nil
}

func (l *LibvirtBackend) forEach(vms []string, action string, _ any) error {
	for _, vm := range vms {
		domain := l.domain(vm)
		fmt.Printf("==> virsh %s %s\n", action, domain)
		if err := l.virsh(action, domain); err != nil {
			return fmt.Errorf("%s %s: %w", action, domain, err)
		}
	}
	return nil
}

func (l *LibvirtBackend) virshInstall(args ...string) error {
	full := append([]string{"--connect", libvirtURI, "--import", "--noautoconsole"}, args...)
	cmd := exec.Command("virt-install", full...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (l *LibvirtBackend) virsh(args ...string) error {
	cmd := exec.Command("virsh", append([]string{"-c", libvirtURI}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (l *LibvirtBackend) domainExists(domain string) bool {
	return l.virsh("domid", domain) == nil
}

func (l *LibvirtBackend) networkName() string {
	if l.conf.Network != "" {
		return l.conf.Network
	}
	return "default"
}

func (l *LibvirtBackend) osVariant() string {
	if l.conf.OSVariant != "" {
		return l.conf.OSVariant
	}
	return "fedora40"
}

// createDiskOverlay makes a small qcow2 overlay backed by the source image;
// bootc switch/rollback writes land in the overlay, the source image stays
// pristine (same trick as container image layers).
func createDiskOverlay(source, target string) error {
	absSource, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("qemu-img"); err == nil {
		cmd := exec.Command("qemu-img", "create", "-f", "qcow2", "-F", "qcow2",
			"-b", absSource, target, fmt.Sprintf("%dG", libvirtDiskSizeGB))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("qemu-img create: %s: %w", string(out), err)
		}
		return nil
	}
	// fallback: plain copy
	data, err := os.ReadFile(absSource)
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o644)
}

func graphicsFlag(gui bool) string {
	if gui {
		return "--graphics=vnc"
	}
	return "--graphics=none"
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
