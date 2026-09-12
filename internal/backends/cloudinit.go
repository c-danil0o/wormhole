package backends

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// NoCloud seed generation: cloud-init picks up user-data/meta-data from a
// volume labelled `cidata` on first boot. The user-data writes
// /var/lib/c12s/node.env — the node's control-plane configuration, which
// persists across every bootc switch and rollback (decision D5/D7.2).
//
// When the VM is intended to host VMs (vmNetwork set), the seed also defines
// and starts a dedicated libvirt network inside the guest. It must NOT be the
// libvirt "default" network: the guest's own NIC already sits on the outer
// 192.168.122.0/24, so a second interface on the same subnet would break the
// guest's routing. The reconciler's VM_IP_BASE/VM_NETWORK env point at it.

type seedSpec struct {
	dir         string
	domain      string
	mac         string
	address     string
	gateway     string
	natsAddress string
	extras      map[string]string
	vmNetwork   string
	vmIpBase    string
}

func buildSeed(spec seedSpec) error {
	metaData := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", spec.domain, spec.domain)

	// Read host SSH public keys for passwordless access from the lab host.
	sshKeys := collectSSHPubKeys()

	env := strings.Join(nodeEnvLines(spec.natsAddress, spec.extras), "\n")
	writeFiles := fmt.Sprintf(`  - path: /var/lib/c12s/node.env
    permissions: '0644'
    content: |
%s
`, indent(env, "      "))

	runcmd := "  - systemctl enable --now serial-getty@ttyS0.service"
	if spec.vmNetwork != "" {
		writeFiles += fmt.Sprintf(`  - path: /var/lib/c12s/vm-network.xml
    permissions: '0644'
    content: |
%s
`, indent(vmNetworkXML(spec.vmNetwork, spec.vmIpBase), "      "))
		runcmd += fmt.Sprintf(`
  - |
    if command -v virsh >/dev/null 2>&1; then
      virsh net-define /var/lib/c12s/vm-network.xml || true
      virsh net-start %s || true
      virsh net-autostart %s || true
    fi`, spec.vmNetwork, spec.vmNetwork)
	}

	userData := fmt.Sprintf(`#cloud-config
users:
  - name: c12s
    gecos: c12s debug user
    groups: [wheel]
    shell: /bin/bash
    lock_passwd: false
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
%s
chpasswd:
  expire: false
  users:
    - name: c12s
      password: c12s
      type: text
ssh_pwauth: true
write_files:
%s
runcmd:
%s
`, indent(strings.Join(sshKeys, "\n"), "      "), writeFiles, runcmd)

	// explicit network config (NoCloud seed, format v1): match the NIC by
	// the deterministic MAC wormhole assigns, static address per VM name.
	// Static (not DHCP): the lab firewall drops DHCP broadcasts on the
	// libvirt bridge while unicast passes; static also gives every node a
	// stable, reproducible address for the experiments.
	networkConfig := fmt.Sprintf(`version: 1
config:
  - type: physical
    name: nic0
    mac_address: "%s"
    subnets:
      - type: static
        address: %s
        netmask: 255.255.255.0
        gateway: %s
`, spec.mac, spec.address, spec.gateway)

	metaPath := filepath.Join(spec.dir, "meta-data")
	userPath := filepath.Join(spec.dir, "user-data")
	netPath := filepath.Join(spec.dir, "network-config")
	if err := os.WriteFile(metaPath, []byte(metaData), 0o644); err != nil {
		return fmt.Errorf("write meta-data: %w", err)
	}
	if err := os.WriteFile(userPath, []byte(userData), 0o644); err != nil {
		return fmt.Errorf("write user-data: %w", err)
	}
	if err := os.WriteFile(netPath, []byte(networkConfig), 0o644); err != nil {
		return fmt.Errorf("write network-config: %w", err)
	}

	seedPath := filepath.Join(spec.dir, "seed.iso")
	cmd := exec.Command(
		"xorriso", "-as", "mkisofs",
		"-o", seedPath,
		"-volid", "cidata",
		"-joliet", "-rock",
		metaPath, userPath, netPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build seed iso (xorriso): %s: %w", string(out), err)
	}
	return nil
}

// vmNetworkXML is the libvirt network the guest's Star reconciler uses for
// the VMs it creates (VM_NETWORK/VM_IP_BASE env in node.env match it).
func vmNetworkXML(name, ipBase string) string {
	return fmt.Sprintf(`<network>
  <name>%s</name>
  <bridge name='virbr-c12s'/>
  <forward mode='nat'/>
  <ip address='%s.1' netmask='255.255.255.0'>
    <dhcp>
      <range start='%s.2' end='%s.254'/>
    </dhcp>
  </ip>
</network>
`, name, ipBase, ipBase, ipBase)
}

// deterministicMAC maps a VM name to a stable locally-administered MAC in
// the QEMU/KVM OUI (52:54:00), so the seed's network-config can match the
// NIC before the VM exists and virt-install can pre-assign the same MAC.
func deterministicMAC(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	b := h.Sum32()
	return fmt.Sprintf("52:54:00:c1:2e:%02x", b&0xFF)
}

func nodeEnvLines(natsAddress string, extras map[string]string) []string {
	lines := []string{
		"NATS_ADDRESS=" + natsAddress,
		"NODE_ID_DIR_PATH=/var/lib/c12s",
		"NODE_ID_FILE_NAME=nodeid",
		"REGISTRATION_REQ_TIMEOUT_MILLISECONDS=10000",
		"MAX_REGISTRATION_RETRIES=3",
		"BIND_ADDRESS=0.0.0.0",
		"BIND_PORT=7946",
		"STAR_ADDRESS=:11000",
		"OS_POLL_INTERVAL=30s",
	}
	for k, v := range extras {
		lines = append(lines, k+"="+v)
	}
	return lines
}

// collectSSHPubKeys reads ~/.ssh/id_*.pub files so cloud-init can inject
// them into the VM user's authorized_keys. Returns YAML list items
// (each prefixed with "- ").
func collectSSHPubKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{"- \"\""}
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".ssh", "id_*.pub"))
	var keys []string
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		key := strings.TrimSpace(string(data))
		if key != "" {
			keys = append(keys, fmt.Sprintf("- %s", key))
		}
	}
	if len(keys) == 0 {
		return []string{"- \"\""}
	}
	return keys
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}
