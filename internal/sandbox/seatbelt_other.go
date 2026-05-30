//go:build !darwin

package sandbox

// Command runs the command unwrapped: no OS sandbox is implemented for this
// platform yet (Linux bubblewrap/landlock is the next step). The permission
// layer still gates the call.
func Command(spec Spec, shell, command string) ([]string, bool) {
	return []string{shell, "-c", command}, false
}

// Available reports that no OS sandbox is available on this platform.
func Available() bool { return false }
