package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandUnwrappedWhenOff(t *testing.T) {
	argv, wrapped := Command(Spec{Mode: "off"}, "bash", "echo hi")
	if wrapped {
		t.Error("Mode=off should not wrap")
	}
	if len(argv) != 3 || argv[0] != "bash" || argv[1] != "-c" || argv[2] != "echo hi" {
		t.Errorf("argv = %v, want [bash -c echo hi]", argv)
	}
}

func TestProfileNetworkAndRoots(t *testing.T) {
	with := seatbeltProfile(Spec{Mode: "enforce", WriteRoots: []string{"/work/proj"}, Network: true})
	if strings.Contains(with, "(deny network*)") {
		t.Error("network=true should not deny network")
	}
	if !strings.Contains(with, "(allow default)") || !strings.Contains(with, "(deny file-write*)") {
		t.Error("profile missing base allow/deny structure")
	}
	if !strings.Contains(with, `(subpath "/work/proj")`) {
		t.Errorf("profile missing the write-root subpath:\n%s", with)
	}
	without := seatbeltProfile(Spec{Mode: "enforce", Network: false})
	if !strings.Contains(without, "(deny network*)") {
		t.Error("network=false should deny network")
	}
}

// TestSandboxEnforcesWrites runs real commands through sandbox-exec and checks
// the boundary: a write under a write-root succeeds, a write elsewhere under
// $HOME (not a root, not a cache dir) is refused, and reads are unrestricted.
// Dirs are created under $HOME (not /tmp, which the profile always allows) so
// the test exercises the root mechanism itself.
func TestSandboxEnforcesWrites(t *testing.T) {
	if !Available() {
		t.Skip("sandbox-exec not available")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	workRoot, err := os.MkdirTemp(home, ".reasonix-sbtest-work-*")
	if err != nil {
		t.Skipf("cannot create work dir under home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(workRoot) })
	outside, err := os.MkdirTemp(home, ".reasonix-sbtest-out-*")
	if err != nil {
		t.Skipf("cannot create outside dir under home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(outside) })

	spec := Spec{Mode: "enforce", WriteRoots: []string{workRoot}, Network: true}
	run := func(command string) error {
		argv, wrapped := Command(spec, "bash", command)
		if !wrapped {
			t.Fatalf("expected wrapping for command %q", command)
		}
		return exec.Command(argv[0], argv[1:]...).Run()
	}

	// Write inside the root: allowed.
	inFile := filepath.Join(workRoot, "in.txt")
	if err := run("echo hi > " + inFile); err != nil {
		t.Fatalf("write inside root failed: %v", err)
	}
	if _, err := os.Stat(inFile); err != nil {
		t.Errorf("file not created inside root: %v", err)
	}

	// Write outside every root: refused (the command exits non-zero).
	outFile := filepath.Join(outside, "out.txt")
	if err := run("echo nope > " + outFile); err == nil {
		t.Error("write outside root should be denied by the sandbox")
	}
	if _, err := os.Stat(outFile); !os.IsNotExist(err) {
		t.Error("file outside root must not be created")
	}

	// Reading outside the root is allowed (read-all).
	if err := run("cat /etc/hosts > " + filepath.Join(workRoot, "hosts.txt")); err != nil {
		t.Errorf("read of /etc/hosts inside sandbox failed: %v", err)
	}
}

// TestGoBuildUnderSandbox guards the default-on profile against the main risk:
// breaking the toolchain. `go build` writes to GOCACHE (under ~/Library/Caches)
// and a temp work dir, both of which the profile must allow, while output lands
// in the workspace. If this fails, the default profile is too tight.
func TestGoBuildUnderSandbox(t *testing.T) {
	if !Available() {
		t.Skip("sandbox-exec not available")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	work, err := os.MkdirTemp(home, ".reasonix-sbtest-go-*")
	if err != nil {
		t.Skipf("cannot create work dir under home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(work) })
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module sbtest\n\ngo 1.25\n")
	write("main.go", "package main\nfunc main() { println(\"ok\") }\n")

	spec := Spec{Mode: "enforce", WriteRoots: []string{work}, Network: true}
	argv, _ := Command(spec, "bash", "cd "+work+" && go build -o sbtest .")
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("go build under sandbox failed (profile too tight?): %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(work, "sbtest")); err != nil {
		t.Errorf("build output missing: %v", err)
	}
}
