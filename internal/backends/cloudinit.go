package backends

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// NoCloud seed generation: cloud-init picks up user-data/meta-data from a
// volume labelled `cidata` on first boot. The user-data writes
// /var/lib/c12s/node.env — the node's control-plane configuration, which
// persists across every bootc switch and rollback (decision D5/D7.2).

func buildSeed(dir, domainName, natsAddress string, extras map[string]string) error {
	metaData := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", domainName, domainName)

	env := strings.Join(nodeEnvLines(natsAddress, extras), "\n")
	userData := fmt.Sprintf(`#cloud-config
write_files:
  - path: /var/lib/c12s/node.env
    permissions: '0644'
    content: |
%s
runcmd:
  - chown -R root:root /var/lib/c12s
`, indent(env, "      "))

	metaPath := filepath.Join(dir, "meta-data")
	userPath := filepath.Join(dir, "user-data")
	if err := os.WriteFile(metaPath, []byte(metaData), 0o644); err != nil {
		return fmt.Errorf("write meta-data: %w", err)
	}
	if err := os.WriteFile(userPath, []byte(userData), 0o644); err != nil {
		return fmt.Errorf("write user-data: %w", err)
	}

	seedPath := filepath.Join(dir, "seed.iso")
	cmd := exec.Command(
		"xorriso", "-as", "mkisofs",
		"-o", seedPath,
		"-volid", "cidata",
		"-joliet", "-rock",
		metaPath, userPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build seed iso (xorriso): %s: %w", string(out), err)
	}
	return nil
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

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}
