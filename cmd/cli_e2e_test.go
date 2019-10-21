package cmd

import (
	"bytes"
	"fmt"
	"github.com/gqlc/gqlc/doc"
	"github.com/gqlc/gqlc/golang"
	"github.com/gqlc/gqlc/js"
	"github.com/spf13/afero"
	"io/ioutil"
	"os"
	"testing"
)

type goldenSuite struct {
	name           string
	input, ex, out string
}

var GOLDENS = []goldenSuite{
	{
		name:  "doc",
		input: "../doc/test.gql",
		ex:    "../doc/test.md",
		out:   "out/test.md",
	},
	{
		name:  "go",
		input: "../golang/test.gql",
		ex:    "../golang/test.gotxt",
		out:   "out/test.go",
	},
	{
		name:  "js",
		input: "../js/test.gql",
		ex:    "../js/test.js",
		out:   "out/test.js",
	},
}

// TestE2E takes each generators golden test file and runs it through
// as if someone was using gqlc in a terminal.
//
func TestE2E(t *testing.T) {
	fs := afero.NewMemMapFs()

	err := initFs(fs, GOLDENS)
	if err != nil {
		t.Error(err)
		return
	}

	cli := NewCLI(WithFS(fs))
	cli.AllowPlugins("gqlc-gen-")

	// Register Documentation generator
	cli.RegisterGenerator(new(doc.Generator),
		"doc_out",
		"doc_opt",
		"Generate Documentation from GraphQL schema.",
	)

	// Register Go generator
	cli.RegisterGenerator(new(golang.Generator),
		"go_out",
		"go_opt",
		"Generate Go source.",
	)

	// Register Javascript generator
	cli.RegisterGenerator(new(js.Generator),
		"js_out",
		"js_opt",
		"Generate Javascript source.",
	)

	for _, gold := range GOLDENS {
		args := []string{
			"gqlc",
			fmt.Sprintf("--%s_out", gold.name), fmt.Sprintf("out"),
			gold.input[3:],
		}

		if err = cli.Run(args); err != nil {
			t.Error(err)
			return
		}

		ex, err := afero.ReadFile(fs, gold.ex[2:])
		if err != nil {
			t.Error(err)
			return
		}

		out, err := afero.ReadFile(fs, gold.out)
		if err != nil {
			t.Error(err)
			return
		}

    compareBytes(t, gold.name, ex, out)
	}
}

func initFs(fs afero.Fs, goldens []goldenSuite) (err error) {
	for _, gold := range GOLDENS {
		dname := gold.name
		if dname == "go" {
			dname = "golang"
		}

		err = fs.Mkdir(dname, os.ModeDir)
		if err != nil {
			return fmt.Errorf("unexpected error when creating directory for: %s:%w", gold.name, err)
		}

		b, err := ioutil.ReadFile(gold.input)
		if err != nil {
			return fmt.Errorf("unexpected error when reading golden input file: %s:%w", gold.input, err)
		}
		if err = afero.WriteFile(fs, gold.input[2:], b, os.ModePerm); err != nil {
			return fmt.Errorf("unexpected error when writing gold input to afero.Fs: %s:%w", gold.input[2:], err)
		}

		b, err = ioutil.ReadFile(gold.ex)
		if err != nil {
			return fmt.Errorf("unexpected error when reading golden output file: %s:%w", gold.ex, err)
		}
		if err = afero.WriteFile(fs, gold.ex[2:], b, os.ModePerm); err != nil {
			return fmt.Errorf("unexpected error when writing gold ex to afero.Fs: %s:%w", gold.ex[2:], err)
		}
	}

	return
}

// compareBytes is a helper for comparing expected output to generated output
func compareBytes(t *testing.T, name string, ex, out []byte) {
	if bytes.EqualFold(out, ex) {
		return
	}

	line := 1
	for i, b := range out {
		if b == '\n' {
			line++
		}

		if ex[i] != b {
			t.Fatalf("%s generator expected: %s, but got: %s, %d:%d", name, string(ex[i]), string(b), i, line)
		}
	}
}
