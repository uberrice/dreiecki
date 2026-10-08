package main

import (
	"flag"
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		pos  []string
		out  string
		pcb  bool
	}{
		{[]string{"HEAD~1"}, []string{"HEAD~1"}, "", false},
		{[]string{"-pcb", "-o", "x.html", "HEAD~1"}, []string{"HEAD~1"}, "x.html", true},
		{[]string{"v7..v8", "-o", "review.html", "--pcb"}, []string{"v7..v8"}, "review.html", true},
		{[]string{"main", "-pcb", "feature"}, []string{"main", "feature"}, "", true},
		{[]string{"-o", "x.html", "--", "-weird", "-pcb"}, []string{"-weird", "-pcb"}, "x.html", false},
		{nil, nil, "", false},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		out := fs.String("o", "", "")
		pcb := fs.Bool("pcb", false, "")
		pos := parseArgs(fs, tc.args)
		if !reflect.DeepEqual(pos, tc.pos) || *out != tc.out || *pcb != tc.pcb {
			t.Errorf("%q: got %q -o %q -pcb %v, want %q -o %q -pcb %v", tc.args, pos, *out, *pcb, tc.pos, tc.out, tc.pcb)
		}
	}
}
