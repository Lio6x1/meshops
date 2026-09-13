package main

import (
	"strings"
	"testing"
)

func TestPasswordInputIsBoundedStrictAndPreservesSpaces(t *testing.T) {
	p, e := readPassword(strings.NewReader(`{"password":"  密码 preserved spaces  "}`))
	if e != nil || p != "  密码 preserved spaces  " {
		t.Fatal(p, e)
	}
	for _, raw := range []string{`{"password":"secret","role":"admin"}`, `{"password":"secret"} {}`, strings.Repeat(" ", 4097), `{"password":null}`} {
		if _, e := readPassword(strings.NewReader(raw)); e == nil {
			t.Fatal("invalid stdin accepted")
		}
	}
}
