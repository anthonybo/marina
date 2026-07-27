package scan

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		fileType, name string
		want           OutputKind
		wantPath       string
	}{
		{"REG", "/Users/me/.local/share/marina/launches/app.log", OutputFile, "/Users/me/.local/share/marina/launches/app.log"},
		{"CHR", "/dev/ttys004", OutputTTY, "/dev/ttys004"},
		{"CHR", "/dev/null", OutputDiscarded, ""},
		{"unix", "->0xe02d86e3d2a2d8e7", OutputPipe, ""},
		{"PIPE", "->0x1234", OutputPipe, ""},
		{"FIFO", "/tmp/fifo", OutputPipe, ""},
		{"IPv4", "*:3000", OutputUnknown, ""},
	}
	for _, tc := range cases {
		got := classify(tc.fileType, tc.name)
		if got.Kind != tc.want || got.Path != tc.wantPath {
			t.Errorf("classify(%q, %q) = %+v, want kind=%s path=%q",
				tc.fileType, tc.name, got, tc.want, tc.wantPath)
		}
	}
}

func TestOutputReadable(t *testing.T) {
	if !(Output{Kind: OutputFile, Path: "/tmp/a.log"}).Readable() {
		t.Error("a regular file must be readable")
	}
	for _, o := range []Output{
		{Kind: OutputFile}, // no path
		{Kind: OutputTTY, Path: "/dev/ttys004"},
		{Kind: OutputPipe},
		{Kind: OutputDiscarded},
		{Kind: OutputUnknown},
	} {
		if o.Readable() {
			t.Errorf("%+v must not be readable", o)
		}
	}
}
