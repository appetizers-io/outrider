package proc

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The test binary doubles as the command under test.
func TestMain(m *testing.M) {
	switch os.Getenv("PROC_TEST_MODE") {
	case "sleep":
		time.Sleep(5 * time.Second)
	case "fail":
		_, _ = os.Stderr.WriteString("boom")
		os.Exit(3)
	case "echo":
		buf := make([]byte, 100)
		n, _ := os.Stdin.Read(buf)
		_, _ = os.Stdout.Write(buf[:n])
	default:
		os.Exit(m.Run())
	}
	os.Exit(0)
}

func self(t *testing.T, mode string) Cmd {
	t.Helper()
	t.Setenv("PROC_TEST_MODE", mode)
	return Cmd{Args: []string{os.Args[0]}}
}

func TestExec(t *testing.T) {
	r := require.New(t)
	c := self(t, "echo")
	c.Stdin = "hello"
	res, err := Exec(t.Context(), c)
	r.NoError(err)
	r.Equal("hello", res.Stdout)

	res, err = Exec(t.Context(), self(t, "fail"))
	var pe *Error
	r.True(errors.As(err, &pe))
	r.Equal(3, res.Code)
	r.Contains(err.Error(), "boom")
}

func TestTimeoutIsAnError(t *testing.T) {
	c := self(t, "sleep")
	c.Timeout = 200 * time.Millisecond
	_, err := Exec(t.Context(), c)
	var pe *Error
	require.True(t, errors.As(err, &pe))
	require.Contains(t, pe.Stderr, "timed out")
}

func TestMissingBinaryIsAnError(t *testing.T) {
	_, err := Exec(t.Context(), Cmd{Args: []string{"definitely-not-installed-xyz"}})
	var pe *Error
	require.True(t, errors.As(err, &pe))
	require.Equal(t, -1, pe.Code)
}
