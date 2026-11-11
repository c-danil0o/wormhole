# wormhole — local VM provisioning CLI

Turns a qcow2 bootc disk into running lab machines: creates libvirt VMs with
a NoCloud seed that writes the per-node `/var/lib/c12s/node.env`, then lets the VMs boot
and enroll into the control plane by themselves. A **lab/dev tool** — it is
explicitly not part of the control plane's delivery path (first install is
out of CP scope; see the thesis plan's provisioning boundary).

## Role in c12s

| Task | How |
|---|---|
| Plain bootc node (`host-1`) | `wormhole -c config.host.yaml create` — a plain bootc node; no libvirt, no nested VMs |
| Base node with libvirt capability (`node-1..N`) | `wormhole -c config.libvirt.yaml create` — runs the VM reconciler and its own nested VMs |
| Legacy Vagrant nodes | `backendType: "vagrant"` — plain Ubuntu+Docker VMs; **cannot test bootc** |
| Provisioning data | NoCloud seed: `/var/lib/c12s/node.env` (per-node values only; the CP addresses ship in the image at `/usr/lib/c12s/node.env`), optional guest-side libvirt network XML, login user from the config's `login` block (default `c12s`/`c12s` plus your `~/.ssh/id_*.pub`) |

VMs created this way are ordinary c12s nodes: from the control plane's point
of view nothing distinguishes them from hand-provisioned hardware.

## Config files in this repo

| file | VMs | addresses |
|------|-----|-----------|
| `config.host.yaml` | `host-1` — plain bootc node | `ipOffset` 50 → `192.168.122.51` |
| `config.libvirt.yaml` | `node-1..N` — base with libvirt capability, runs the VM reconciler | `ipOffset` 60 → `192.168.122.61..` |

- `diskImage` must be built from edge-base v3.10 or newer (Supernova's
  `BuildQCOW2` / `cockpit image build qcow2`): older disks lack greenboot's
  GRUB snippet on `/boot`, and a failing update then reboots forever instead
  of rolling back.
- VM names are `<nameBase>-<n>`; the address is `ipBase + ipOffset + n` and
  the MAC is derived from it. Keep `ipOffset` ranges disjoint across configs —
  `create` refuses an address another VM in the state dir already holds.
- Domains live in `qemu:///system`. Plain `virsh` as a normal user talks to
  `qemu:///session` and shows nothing — use `virsh -c qemu:///system ...`.

## Build and usage

```bash
go build -o wormhole .

wormhole -c config.host.yaml create            # no names = every VM in the config
wormhole -c config.host.yaml create host-1     # one VM
wormhole -c config.host.yaml destroy host-1    # delete VM + all its data
```

`--config/-c` defaults to `config.yaml`. All lifecycle commands accept VM
names; without them they act on **all** VMs from the config:

| Command | Effect |
|---|---|
| `create` | create VM(s); skips ones that already exist (safe to re-run) |
| `stop` / `resume` | suspend / resume (libvirt backend: domain save/restore semantics) |
| `shutdown` | graceful power-off |
| `reload` | recreate from config (Vagrant backend: re-reads the Vagrantfile) |
| `destroy` | delete VM and its disks |
| `setup` | Vagrant-only: generate the Vagrantfile from YAML |
| `start-nodes` / `stop-nodes` | run `/vagrant/node_start.sh` / `node_stop.sh` inside Vagrant VMs |

Requirements for the libvirt backend: `virt-install`, `virsh`, `qemu-img`,
`xorriso`, and a user in the `libvirt` group.

## Configuration (libvirt backend)

All fields in the YAML (`internal/config`): `vmCount`, `cpus`, `memory`,
`nameBase`, `backendType`, `diskImage`, `network`, `osVariant`, `uefi`,
`cpuModel` (`host-passthrough` needed for nested KVM), `stateDir`,
`natsAddress` (optional override of the image's NATS address), `ipBase`, `ipOffset`, `vmNetwork` /
`vmIpBase` (guest-side network for VMs the node will host), `extras`
(extra per-node `node.env` pairs), `login` (the user the seed creates:
`user`, `password` — `""` disables password login —, `sshKeys` as `.pub`
paths or literal keys; without the block: `c12s`/`c12s` plus your
`~/.ssh/id_*.pub`). Neither a password nor a key is rejected before any VM is
created.

## Status

Stable for the lab provisioning workflow (libvirt backend). The Vagrant
backend is retained but legacy — it cannot run bootc nodes.

## Bugs, disabled and unfinished features

### Confirmed bugs

| Where | Effect |
|---|---|
| README/install instructions historically pointed at personal forks (`radepejanvic/wormhole`) and `control_plane_start.sh` — fixed here. |
| Vagrant backend is legacy | Its VMs are plain Ubuntu + Docker (no OSTree/greenboot); OS-update experiments silently can't run there. Use the libvirt backend for anything bootc. |

### Disabled / unfinished features

- **No test suite** (`go test ./...` → no test files); behavior is verified
  by lab runs.
- The guest `ssh -J` (ProxyJump through `host-1` to nested VMs) **fails** —
  host-1's sshd refuses every forward, cause not found. Workaround used by
  the lab: serial-console helper (`tools/experiments/vm_console.py`,
  `source common.sh; vm_run vm-01 '<cmd>'`).
- Dockerized "simulated" nodes (`tools/node.yml`, `NODES_NUM`) share nothing
  with this tool's state; they exist for CP-logic testing only.

### PoC boundaries (by design, not bugs)

- Provisioning is external/opaque to the control plane by thesis scope: the
  CP starts at first boot of an already-prepared machine.
- No TLS/auth anywhere: lab networks only.
