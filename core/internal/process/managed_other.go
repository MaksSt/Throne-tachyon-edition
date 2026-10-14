//go:build !windows && !linux

package process

func startManagedChild(path string, args []string, noOut bool) (running, error) {
	return startChild(path, args, noOut)
}
