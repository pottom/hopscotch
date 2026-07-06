package cmd

import (
	"bufio"
	"strings"
	"testing"
	"time"
)

// TestFieldString_EOFWithData is a regression test for the bug where
// ReadString('\n') returns valid data alongside io.EOF (e.g. the last line of
// piped input with no trailing newline), and the old code discarded that
// data on any non-nil error instead of only on a real read failure.
func TestFieldString_EOFWithData(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("myhost")) // no trailing newline
	got, err := fieldString(r, "Host", "", false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "myhost" {
		t.Errorf("got %q, want %q", got, "myhost")
	}
}

func TestFieldString_EOFEmptyRequiredReturnsError(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))

	done := make(chan struct{})
	var err error
	go func() {
		_, err = fieldString(r, "Host", "", false, true)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fieldString looped forever on a required field at EOF instead of returning an error")
	}
	if err == nil {
		t.Fatal("expected an error for a required field at EOF, got nil")
	}
}

func TestFieldInt_EOFWithData(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("2222")) // no trailing newline
	got, err := fieldInt(r, "Port", 22, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 2222 {
		t.Errorf("got %d, want 2222", got)
	}
}

func TestFieldInt_EOFEmptyUsesDefault(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	got, err := fieldInt(r, "Port", 22, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 22 {
		t.Errorf("got %d, want default 22", got)
	}
}

func TestFieldInt_EOFWithGarbageReturnsError(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("not-a-number")) // no trailing newline

	done := make(chan struct{})
	var err error
	go func() {
		_, err = fieldInt(r, "Port", 22, false)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fieldInt looped forever on unparseable input at EOF instead of returning an error")
	}
	if err == nil {
		t.Fatal("expected an error for unparseable input at EOF, got nil")
	}
}
