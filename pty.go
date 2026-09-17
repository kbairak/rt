package main

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

func spawnPTY(shell string, ws pty.Winsize, env []string, args []string) (master *os.File, cmd *exec.Cmd, err error) {
	cmd = exec.Command(shell, args...)
	cmd.Env = env
	master, err = pty.StartWithSize(cmd, &ws)
	if err != nil {
		return nil, nil, err
	}
	return master, cmd, nil
}

func resizePTY(master *os.File, rows, cols uint16) error {
	return pty.Setsize(master, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}
