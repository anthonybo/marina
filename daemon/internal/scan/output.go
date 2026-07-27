package scan

import (
	"context"
	"strconv"
	"strings"
)

// OutputKind says what a process's stdout is connected to, which determines
// whether its output can be read from outside the process.
type OutputKind string

const (
	// OutputFile is a regular file: readable by anyone, so Marina can tail it.
	OutputFile OutputKind = "file"
	// OutputTTY is a terminal. The bytes go to that terminal and nowhere else.
	OutputTTY OutputKind = "tty"
	// OutputPipe is a pipe or socket, already being consumed by whatever is on
	// the other end — usually the terminal emulator that started the process.
	// Point-to-point, so there is nothing left for a third party to read.
	OutputPipe OutputKind = "pipe"
	// OutputDiscarded is /dev/null.
	OutputDiscarded OutputKind = "discarded"
	// OutputUnknown covers anything else, including a process we cannot inspect.
	OutputUnknown OutputKind = "unknown"
)

// Output describes where a process writes its output.
type Output struct {
	Kind OutputKind `json:"kind"`
	// Path is set for a file or tty, so the UI can name it.
	Path string `json:"path,omitempty"`
}

// Readable reports whether Marina could show this output to the user.
func (o Output) Readable() bool { return o.Kind == OutputFile && o.Path != "" }

// Outputs resolves stdout (falling back to stderr) for each PID in one lsof call.
//
// This is what lets Marina answer "why can't I see this app's terminal?" with a
// specific reason rather than a shrug — and lets it read the output of a process
// it did not start, when that process happens to write to a file.
func Outputs(ctx context.Context, pids []int) map[int]Output {
	outputs := make(map[int]Output, len(pids))
	if len(pids) == 0 {
		return outputs
	}

	// -d 1,2 selects stdout and stderr; -a ANDs it with the pid selection rather
	// than lsof's default OR.
	out, _ := output(ctx, lsofBin, "-a", "-p", joinInts(pids), "-d", "1,2", "-Fftn")

	var (
		curPID int
		curFD  string
		curT   string
	)
	commit := func(name string) {
		if curPID == 0 || curFD == "" {
			return
		}
		// Prefer stdout; only let stderr fill a gap.
		if existing, seen := outputs[curPID]; seen && curFD != "1" && existing.Kind != OutputUnknown {
			return
		}
		outputs[curPID] = classify(curT, name)
	}

	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			curPID, _ = strconv.Atoi(line[1:])
			curFD, curT = "", ""
		case 'f':
			curFD = line[1:]
			curT = ""
		case 't':
			curT = line[1:]
		case 'n':
			commit(unescape(line[1:]))
		}
	}
	return outputs
}

func classify(fileType, name string) Output {
	switch fileType {
	case "REG":
		return Output{Kind: OutputFile, Path: name}
	case "CHR":
		if name == "/dev/null" {
			return Output{Kind: OutputDiscarded}
		}
		if strings.HasPrefix(name, "/dev/tty") || strings.HasPrefix(name, "/dev/pty") {
			return Output{Kind: OutputTTY, Path: name}
		}
		return Output{Kind: OutputUnknown, Path: name}
	case "PIPE", "FIFO", "unix", "systm":
		return Output{Kind: OutputPipe}
	default:
		return Output{Kind: OutputUnknown}
	}
}
