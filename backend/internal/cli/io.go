package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// IO wraps terminal input and output.
//
// When stdin is not a terminal (a piped script, a test) it stops clearing the
// screen and echoes what it read, so a piped run produces a readable
// transcript instead of a pile of escape codes.
type IO struct {
	in      *bufio.Reader
	out     io.Writer
	clear   bool
	echo    bool
	eof     bool
	retries int
}

func NewIO(noClear bool) *IO {
	tty := false
	if fi, err := os.Stdin.Stat(); err == nil {
		tty = fi.Mode()&os.ModeCharDevice != 0
	}
	return &IO{
		in:    bufio.NewReader(os.Stdin),
		out:   os.Stdout,
		clear: tty && !noClear,
		echo:  !tty,
	}
}

func (t *IO) EOF() bool { return t.eof }

func (t *IO) Printf(format string, a ...any) { fmt.Fprintf(t.out, format, a...) }
func (t *IO) Println(s string)               { fmt.Fprintln(t.out, s) }

func (t *IO) Error(err error) {
	fmt.Fprintf(t.out, "  !! %v\n", err)
}

// ClearScreen wipes the terminal, including scrollback. This is what keeps
// pass-the-device honest: the previous player's private screen must not be
// recoverable by scrolling up.
func (t *IO) ClearScreen() {
	if !t.clear {
		fmt.Fprintln(t.out, "\n"+strings.Repeat("=", 62))
		return
	}
	fmt.Fprint(t.out, "\033[3J\033[H\033[2J")
}

func (t *IO) read(prompt string) string {
	if t.eof {
		return ""
	}
	fmt.Fprint(t.out, prompt)
	line, err := t.in.ReadString('\n')
	if err != nil && line == "" {
		t.eof = true
		fmt.Fprintln(t.out, "\n(input ended)")
		return ""
	}
	line = strings.TrimSpace(line)
	if t.echo {
		fmt.Fprintln(t.out, line)
	}
	return line
}

// Line reads a free-text answer.
func (t *IO) Line(prompt string) string { return t.read(prompt) }

// Pause waits for ENTER. It is the hand-over point in pass-the-device play.
func (t *IO) Pause(msg string) { t.read(msg) }

func (t *IO) fail(msg string) {
	t.retries++
	fmt.Fprintf(t.out, "  ?? %s\n", msg)
	if t.retries > 8 {
		t.eof = true
		fmt.Fprintln(t.out, "(too many invalid inputs, giving up)")
	}
}

// Int reads an integer inside [min, max].
func (t *IO) Int(prompt string, min, max int) int {
	for !t.eof {
		s := t.read(fmt.Sprintf("%s [%d-%d]: ", prompt, min, max))
		if t.eof {
			break
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < min || n > max {
			t.fail("enter a number between " + strconv.Itoa(min) + " and " + strconv.Itoa(max))
			continue
		}
		t.retries = 0
		return n
	}
	return min
}

// Ints reads exactly count distinct integers inside [min, max], space separated.
func (t *IO) Ints(prompt string, count, min, max int) []int {
	for !t.eof {
		s := t.read(fmt.Sprintf("%s (%d values, space separated) [%d-%d]: ", prompt, count, min, max))
		if t.eof {
			break
		}
		fields := strings.Fields(strings.ReplaceAll(s, ",", " "))
		if len(fields) != count {
			t.fail(fmt.Sprintf("need exactly %d values", count))
			continue
		}
		out := make([]int, 0, count)
		seen := map[int]bool{}
		ok := true
		for _, f := range fields {
			n, err := strconv.Atoi(f)
			if err != nil || n < min || n > max || seen[n] {
				ok = false
				break
			}
			seen[n] = true
			out = append(out, n)
		}
		if !ok {
			t.fail("values must be distinct and in range")
			continue
		}
		t.retries = 0
		return out
	}
	out := make([]int, 0, count)
	for i := 0; i < count && min+i <= max; i++ {
		out = append(out, min+i)
	}
	return out
}

func (t *IO) YesNo(prompt string) bool {
	for !t.eof {
		s := strings.ToLower(t.read(prompt + " (y/n): "))
		if t.eof {
			break
		}
		switch s {
		case "y", "yes":
			t.retries = 0
			return true
		case "n", "no":
			t.retries = 0
			return false
		}
		t.fail("answer y or n")
	}
	return false
}

// Option is one entry in a menu.
type Option struct {
	Key   string
	Label string
}

// Choose prints a menu and returns the chosen key.
func (t *IO) Choose(prompt string, opts []Option) string {
	for !t.eof {
		t.Println("")
		for _, o := range opts {
			t.Printf("  [%s] %s\n", o.Key, o.Label)
		}
		s := strings.ToLower(t.read(prompt + ": "))
		if t.eof {
			break
		}
		for _, o := range opts {
			if s == strings.ToLower(o.Key) {
				t.retries = 0
				return o.Key
			}
		}
		t.fail("pick one of the listed keys")
	}
	if len(opts) > 0 {
		return opts[len(opts)-1].Key
	}
	return ""
}
