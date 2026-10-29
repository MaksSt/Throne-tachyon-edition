//go:build !windows

package process

func startEngine(path string, args []string, output *engineOutput) (running, error) {
	cmd := newCmd(path, args, false)
	cmd.Stdout = output
	cmd.Stderr = output
	return startCmd(cmd)
}
