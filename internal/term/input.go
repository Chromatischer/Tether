package term

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

type lineEditor struct {
	buf      []rune
	pos      int
	termFD   int
	oldState *term.State
	rawMode  bool
	profile  TerminalProfile
}

func newLineEditor(profile TerminalProfile) (*lineEditor, error) {
	le := &lineEditor{
		termFD:  int(os.Stdin.Fd()),
		profile: profile,
	}

	if profile.TextMode || !profile.IsTTY {
		return le, nil
	}

	oldState, err := term.MakeRaw(le.termFD)
	if err != nil {
		return le, nil
	}
	le.oldState = oldState
	le.rawMode = true

	return le, nil
}

func (le *lineEditor) Close() {
	if le.rawMode && le.oldState != nil {
		term.Restore(le.termFD, le.oldState)
		le.rawMode = false
	}
}

func (le *lineEditor) LeaveRawMode() {
	if le.rawMode && le.oldState != nil {
		term.Restore(le.termFD, le.oldState)
		le.rawMode = false
		le.oldState = nil
	}
}

func (le *lineEditor) EnterRawMode() {
	if le.profile.TextMode || !le.profile.IsTTY {
		return
	}
	oldState, err := term.MakeRaw(le.termFD)
	if err == nil {
		le.oldState = oldState
		le.rawMode = true
	}
}

func (le *lineEditor) ReadLine() (string, error) {
	if le.rawMode {
		return le.readLineRaw()
	}
	return le.readLinePlain()
}

func (le *lineEditor) readLinePlain() (string, error) {
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("EOF")
	}
	return strings.TrimSpace(scanner.Text()), nil
}

func (le *lineEditor) readLineRaw() (string, error) {
	le.buf = le.buf[:0]
	le.pos = 0

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	var one [1]byte
	for {
		select {
		case <-sigCh:
			le.updateSize()
			continue
		default:
		}

		n, err := os.Stdin.Read(one[:])
		if err != nil {
			return "", err
		}
		if n == 0 {
			continue
		}

		b := one[0]

		switch {
		case b == 0x1b:
			if handled := le.handleEscape(); handled {
				continue
			}
			return "", fmt.Errorf("interrupted")

		case b == 0x0d || b == 0x0a:
			fmt.Print("\r\n")
			if len(le.buf) > 0 {
				result := strings.TrimRight(string(le.buf), "\r\n")
				le.buf = le.buf[:0]
				le.pos = 0
				return result, nil
			}
			continue

		case b == 0x03:
			return "", fmt.Errorf("interrupted")

		case b == 0x7f || b == 0x08:
			le.backspace()

		case b == 0x0c:
			fmt.Print("\033[2J\033[H")
			le.redraw()

		case b == 0x09:
			continue

		case b >= 32:
			le.insertRune(rune(b))
			le.redraw()

		default:
			continue
		}
	}
}

func (le *lineEditor) handleEscape() bool {
	var one [1]byte
	n, err := readWithTimeout(le.termFD, one[:], 50*time.Millisecond)
	if err != nil || n == 0 {
		return true
	}

	b := one[0]

	switch {
	case b == '\r' || b == '\n':
		le.insertRune('\n')
		fmt.Print("\r\n")
		le.redraw()
		return true

	case b == '[':
		return le.handleCSI()

	default:
		return true
	}
}

func (le *lineEditor) handleCSI() bool {
	var one [1]byte
	n, err := readWithTimeout(le.termFD, one[:], 50*time.Millisecond)
	if err != nil || n == 0 {
		return true
	}

	b := one[0]

	switch b {
	case 'A':
		return true
	case 'B':
		return true
	case 'C':
		if le.pos < len(le.buf) {
			le.pos++
			le.redraw()
		}
		return true
	case 'D':
		if le.pos > 0 {
			le.pos--
			le.redraw()
		}
		return true
	case 'H':
		le.pos = 0
		le.redraw()
		return true
	case 'F':
		le.pos = len(le.buf)
		le.redraw()
		return true
	case '3':
		n2, err2 := readWithTimeout(le.termFD, one[:], 50*time.Millisecond)
		if err2 == nil && n2 > 0 && one[0] == '~' {
			if le.pos < len(le.buf) {
				le.buf = append(le.buf[:le.pos], le.buf[le.pos+1:]...)
				le.redraw()
			}
		}
		return true
	}

	return true
}

func (le *lineEditor) insertRune(r rune) {
	if le.pos == len(le.buf) {
		le.buf = append(le.buf, r)
	} else {
		le.buf = append(le.buf[:le.pos], append([]rune{r}, le.buf[le.pos:]...)...)
	}
	le.pos++
}

func (le *lineEditor) backspace() {
	if le.pos > 0 {
		le.buf = append(le.buf[:le.pos-1], le.buf[le.pos:]...)
		le.pos--
		le.redraw()
	}
}

func (le *lineEditor) redraw() {
	var sb strings.Builder
	sb.WriteString("\r\033[K")

	pal := newPalette(le.profile.ColorLevel)
	prompt := apply(pal.brightWhite(), "You> ")
	sb.WriteString(prompt)

	for _, r := range le.buf {
		if r == '\n' {
			sb.WriteString("\r\n")
			sb.WriteString(strings.Repeat(" ", visualWidth("You> ")))
		} else {
			sb.WriteRune(r)
		}
	}

	fmt.Print(sb.String())

	if le.pos < len(le.buf) {
		offset := le.bufPosOffset()
		fmt.Printf("\033[%dD", offset)
	}
}

func (le *lineEditor) bufPosOffset() int {
	offset := 0
	for i := len(le.buf) - 1; i >= le.pos; i-- {
		r := le.buf[i]
		if r == '\n' {
			// need to move up + back across continuation prefix
			offset += visualWidth("You> ")
		} else {
			offset++
		}
	}
	return offset
}

func (le *lineEditor) updateSize() {
	w, _, err := term.GetSize(le.termFD)
	if err == nil {
		le.profile.Width = w
	}
}

func (le *lineEditor) PasswordPrompt(prompt string) (string, error) {
	if le.rawMode {
		le.Close()
	}
	fmt.Print(prompt)
	password, err := term.ReadPassword(le.termFD)
	fmt.Println()
	if err != nil {
		return "", err
	}
	if le.profile.ColorLevel > ColorNone && le.profile.IsTTY && !le.profile.TextMode {
		oldState, err := term.MakeRaw(le.termFD)
		if err == nil {
			le.oldState = oldState
			le.rawMode = true
		}
	}
	return string(password), nil
}

func readWithTimeout(fd int, buf []byte, timeout time.Duration) (int, error) {
	ch := make(chan struct{})
	var n int
	var err error
	go func() {
		defer close(ch)
		n, err = os.NewFile(uintptr(fd), "/dev/stdin").Read(buf)
	}()
	select {
	case <-ch:
		return n, err
	case <-time.After(timeout):
		return 0, nil
	}
}
