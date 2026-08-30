package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	VMCount      int    `yaml:"vmCount"`
	OSDistro     string `yaml:"osDistro"`
	OSVersion    string `yaml:"osVersion"`
	CPUs         int    `yaml:"cpus"`
	Memory       int    `yaml:"memory"`
	GUI          bool   `yaml:"gui"`
	IPBase       string `yaml:"ipBase"`
	GuestPort    int    `yaml:"guestPort"`
	HostPortBase int    `yaml:"hostPortBase"`
	NameBase     string `yaml:"nameBase"`
	BackendType  string `yaml:"backendType"`

	// libvirt backend
	DiskImage   string            `yaml:"diskImage"`   // source qcow2 the VMs are created from (required)
	Network     string            `yaml:"network"`     // libvirt network, default "default"
	OSVariant   string            `yaml:"osVariant"`   // virt-install --os-variant, default "fedora40"
	StateDir    string            `yaml:"stateDir"`    // per-VM disks + seeds, default ~/.wormhole/libvirt
	NATSAddress string            `yaml:"natsAddress"` // written into /var/lib/c12s/node.env on first boot
	Extras      map[string]string `yaml:"extras"`      // extra key=value pairs for node.env
}

func (conf *Config) Validate() error {
	if conf.VMCount <= 0 {
		return fmt.Errorf("vmCount must be greater than 0")
	}
	if conf.CPUs <= 0 {
		return fmt.Errorf("cpus must be greater than 0")
	}
	if conf.Memory <= 0 {
		return fmt.Errorf("memory must be greater than 0")
	}
	if conf.NameBase == "" {
		return fmt.Errorf("nameBase cannot be empty")
	}
	switch conf.BackendType {
	case "libvirt":
		return conf.validateLibvirt()
	case "vagrant":
		return conf.validateVagrant()
	default:
		return fmt.Errorf("unsupported backend: %s", conf.BackendType)
	}
}

func (conf *Config) validateVagrant() error {
	if conf.OSDistro == "" {
		return fmt.Errorf("osDistro cannot be empty")
	}
	if conf.OSVersion == "" {
		return fmt.Errorf("osVersion cannot be empty")
	}
	if conf.IPBase == "" {
		return fmt.Errorf("ipBase cannot be empty")
	}
	if conf.GuestPort < 1024 || conf.GuestPort > 65535 {
		return fmt.Errorf("guestPort must be between 1024 and 65535")
	}
	if conf.HostPortBase < 1024 || conf.HostPortBase > 65535 {
		return fmt.Errorf("hostPortBase must be between 1024 and 65535")
	}
	return nil
}

func (conf *Config) validateLibvirt() error {
	if conf.DiskImage == "" {
		return fmt.Errorf("diskImage is required for the libvirt backend")
	}
	if _, err := os.Stat(conf.DiskImage); err != nil {
		return fmt.Errorf("diskImage %s: %w", conf.DiskImage, err)
	}
	if conf.NATSAddress == "" {
		return fmt.Errorf("natsAddress is required for the libvirt backend (written into node.env)")
	}
	return nil
}

func LoadFromYaml(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var conf Config
	err = yaml.Unmarshal(data, &conf)
	if err != nil {
		return nil, fmt.Errorf("unmarshal yaml: %w", err)
	}

	err = conf.Validate()
	if err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &conf, nil
}
