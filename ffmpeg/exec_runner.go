package ffmpeg

import (
	"context"
	"io"
	"os/exec"
)

type execRunner struct{}

func (execRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (execRunner) Start(ctx context.Context, path string, args []string, stdin io.Reader, stderr io.Writer) (RunningCommand, error) {
	command := exec.CommandContext(ctx, path, args...)
	command.Stdin = stdin
	command.Stderr = stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		_ = stdout.Close()
		return nil, err
	}
	return &runningCommand{stdout: stdout, command: command}, nil
}

type runningCommand struct {
	stdout  io.ReadCloser
	command *exec.Cmd
}

func (c *runningCommand) Read(p []byte) (int, error) { return c.stdout.Read(p) }
func (c *runningCommand) Close() error               { return c.stdout.Close() }
func (c *runningCommand) Wait() error                { return c.command.Wait() }
func (c *runningCommand) Kill() error                { return c.command.Process.Kill() }

var _ RunningCommand = (*runningCommand)(nil)
