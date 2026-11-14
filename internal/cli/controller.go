package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/takomitkm/dialhome/internal/controller"
	"github.com/takomitkm/dialhome/internal/errs"
	"github.com/takomitkm/dialhome/internal/platform"
)

// controllerPaths resolves the controller config layout.
func controllerPaths() (dir, machines string, err error) {
	dir = platform.DataDir("controller")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	return dir, filepath.Join(dir, "machines.json"), nil
}

func runControllerImpl(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	_, machinesPath, err := controllerPaths()
	if err != nil {
		return err
	}
	reg, err := controller.Load(machinesPath)
	if err != nil {
		return err
	}
	sshExe := "ssh"

	switch args[0] {
	case "list":
		names := reg.Names()
		if len(names) == 0 {
			fmt.Println("(no machines registered -- edit " + machinesPath + ")")
			return nil
		}
		fmt.Printf("%-20s %-10s %-8s %s\n", "NAME", "SHELL", "ANCHORS", "DISPLAY/NOTE")
		for _, n := range names {
			m := reg.Machines[n]
			label := m.Display
			if label == "" {
				label = m.Note
			}
			fmt.Printf("%-20s %-10s %-8d %s\n", n, m.Shell, len(m.Anchors), label)
		}
		return nil

	case "anchors":
		if len(args) < 2 {
			return fmt.Errorf("%w: anchors <machine>", errs.ErrUsage)
		}
		if _, ok := reg.Machines[args[1]]; !ok {
			return fmt.Errorf("machine %q not registered", args[1])
		}
		_, trail, _ := controller.Failover(reg, args[1], sshExe, func(a controller.Anchor) []string {
			return anchorArgs(a)
		})
		fmt.Printf("machine %s anchor trail:\n", args[1])
		for i, t := range trail {
			kind := anchorKind(t.Anchor)
			fmt.Printf("  %d. [%s] %s -> %s\n", i+1, kind, anchorLabel(t.Anchor), verdict(t))
		}
		return nil

	case "status":
		names := reg.Names()
		for _, n := range names {
			_, trail, err := controller.Failover(reg, n, sshExe, func(a controller.Anchor) []string {
				return anchorArgs(a)
			})
			state := "DOWN"
			if err == nil {
				state = "UP"
			}
			last := ""
			if len(trail) > 0 {
				last = trail[len(trail)-1].Anchor.Alias
				if last == "" {
					last = "local-port"
				}
			}
			fmt.Printf("%-20s [%s] via %s\n", n, state, last)
		}
		return nil

	case "shell":
		if len(args) < 2 {
			return fmt.Errorf("%w: shell <machine>", errs.ErrUsage)
		}
		machine := args[1]
		rargs, _, err := controller.Failover(reg, machine, sshExe, func(a controller.Anchor) []string {
			return anchorArgs(a)
		})
		if err != nil {
			return failoverReport(machine, err)
		}
		shellArgs := []string{"-t"}
		shellArgs = append(shellArgs, rargs...)
		// interactive: attach our stdio; ssh allocates the remote PTY
		cmd := exec.Command(sshExe, shellArgs...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()

	case "exec":
		if len(args) < 3 {
			return fmt.Errorf("%w: exec <machine> <command...>", errs.ErrUsage)
		}
		machine := args[1]
		rargs, _, err := controller.Failover(reg, machine, sshExe, func(a controller.Anchor) []string {
			return anchorArgs(a)
		})
		if err != nil {
			return failoverReport(machine, err)
		}
		m := reg.Machines[machine]
		remote := strings.Join(args[2:], " ")
		if m.Shell == "bash" {
			// script via stdin avoids the cmd->ssh->bash quote nesting (baseline §4.3)
			cmd := exec.Command(sshExe, append(rargs, "bash -s")...)
			cmd.Stdin = strings.NewReader(remote + "\n")
			out, err := cmd.CombinedOutput()
			os.Stdout.Write(out)
			return err
		}
		cmd := exec.Command(sshExe, append(rargs, remote)...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()

	default:
		return fmt.Errorf("%w: unknown controller subcommand %q", errs.ErrUsage, args[0])
	}
}

func anchorKind(a controller.Anchor) string {
	switch {
	case a.Alias != "":
		return "ssh-alias"
	case a.Node != "":
		return "dialhome"
	default:
		return "local-port"
	}
}

func anchorLabel(a controller.Anchor) string {
	if a.Alias != "" {
		return a.Alias
	}
	if a.Node != "" {
		return a.Node + " via " + a.RelayAddr
	}
	return fmt.Sprintf("127.0.0.1:%d", a.LocalPort)
}

func verdict(t controller.ProbeResult) string {
	if t.OK {
		return "OK (" + t.Detail + ")"
	}
	return "fail (" + t.Detail + ")"
}

// anchorArgs maps an anchor to the ssh argument vector addressing it.
// ssh-alias anchors reference the user's own ~/.ssh/config (never migrated).
func anchorArgs(a controller.Anchor) []string {
	if a.Alias != "" {
		return []string{a.Alias}
	}
	if a.Node != "" && a.RelayAddr != "" {
		host, port := splitHostPort(a.RelayAddr)
		return []string{"-p", port, host}
	}
	return nil
}

func splitHostPort(addr string) (string, string) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "22"
	}
	return addr[:i], addr[i+1:]
}

func failoverReport(machine string, err error) error {
	fmt.Fprintf(os.Stderr, "machine %s: %v\n", machine, err)
	return fmt.Errorf("%w: all anchors failed (see trail above)", errs.ErrGeneric)
}
