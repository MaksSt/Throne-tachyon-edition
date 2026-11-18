//go:build !windows

package process

func startEngine(path string, args []string, output *engineOutput) (Running, error) {
	cmd := newCmd(childSpec{path: path, args: args, env: childEnv()})
	cmd.Stdout = output
	cmd.Stderr = output
	return startCmd(cmd)
}
