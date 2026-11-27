package mint

import (
	"errors"
	"os"
	"os/exec"
)

// Handover gives the terminal to an interactive program (ssh, an editor, a
// pager, a password prompt) until it ends, then takes it back and redraws.
// The program is run directly, never through a shell, with this program's
// standard input, output and error. It returns the program's exit status.
//
// Call it from an action (OnActivate): keys are read between actions, so
// nothing else reads the keyboard while the program runs.
func Handover(argv []string, dir string, env []string) (int, error) {
	if len(argv) == 0 {
		return -1, errors.New("nothing to run")
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return -1, err
	}
	cmd := &exec.Cmd{Path: path, Args: argv, Dir: dir, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	active := screenActive
	if active {
		ScreenLeave()
	}
	err = cmd.Run()
	if active {
		ScreenEnter()
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return -1, err
	}
	return code, nil
}

// Handover runs an interactive program in the terminal; see Handover.
func (c *Ctx) Handover(argv []string, dir string, env []string) (int, error) {
	return Handover(argv, dir, env)
}
