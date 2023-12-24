package main

import (
	"testing"
)

// TestHelloName calls greetings.Hello with a name, checking
// for a valid return value.
func TestCue(t *testing.T) {
	retCue := fixCueFileCase("test.cue")
	t.Logf("ret: %v", retCue)
}
