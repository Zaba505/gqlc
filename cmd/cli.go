// Package cmd implements the command line interface for gqlc.
package cmd

import (
	"fmt"
	"os"
	"runtime/debug"
	"text/scanner"

	"github.com/gqlc/gqlc/gen"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type option func(*CommandLine)

// WithCommand configures the underlying cobra.Command to be used.
func WithCommand(cmd *cobra.Command) option {
	return func(c *CommandLine) {
		c.Command = cmd
	}
}

// WithFS configures the underlying afero.FS used to read/write files.
func WithFS(fs afero.Fs) option {
	return func(c *CommandLine) {
		c.fs = fs
	}
}

// CommandLine which simply extends a github.com/spf13/cobra.Command
// to include helper methods for registering code generators.
//
type CommandLine struct {
	*cobra.Command

	pluginPrefix *string
	geners       []generator
	outDirs      []string
	fp           *fparser
	fs           afero.Fs
}

// NewCLI returns a CommandLine implementation.
func NewCLI(opts ...option) (c *CommandLine) {
	c = &CommandLine{
		pluginPrefix: new(string),
		fp: &fparser{
			Scanner: new(scanner.Scanner),
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	if c.fs == nil {
		c.fs = afero.NewOsFs()
	}

	if c.Command != nil {
		return
	}

	var l *zap.Logger

	c.Command = rootCmd
	c.PreRunE = chainPreRunEs(
		func(cmd *cobra.Command, args []string) error {
			v, err := cmd.Flags().GetBool("verbose")
			if !v || err != nil {
				return err
			}

			enc := zapcore.NewConsoleEncoder(zapcore.EncoderConfig{
				MessageKey: "msg",
				LevelKey:   "level",
				TimeKey:    "ts",
				NameKey:    "logger",
				CallerKey:  "caller",
			})
			core := zapcore.NewCore(enc, os.Stdout, zap.InfoLevel)

			l = zap.New(core)
			zap.ReplaceGlobals(l)
			return err
		},
		validatePluginTypes(c.fs),
		initGenDirs(c.fs, &c.outDirs),
	)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if l != nil {
			defer l.Sync()
		}

		importPaths, err := cmd.Flags().GetStringSlice("import_path")
		if err != nil {
			return err
		}

		return root(c.fs, c.geners, importPaths, cmd.Flags().Args()...)
	}

	return
}

// AllowPlugins sets the plugin prefix to be used
// when looking up plugin executables.
//
func (c *CommandLine) AllowPlugins(prefix string) { *c.pluginPrefix = prefix }

// RegisterGenerator registers a generator with the compiler.
func (c *CommandLine) RegisterGenerator(g gen.Generator, name, opt, help string) {
	opts := make(map[string]interface{})

	f := genFlag{
		g:       g,
		opts:    opts,
		geners:  &c.geners,
		outDirs: &c.outDirs,
		fp:      c.fp,
	}

	c.Flags().Var(f, name, help)

	if opt != "" {
		f.isOpt = true
		c.Flags().Var(f, opt, "Pass additional options to generator.")
	}
}

func wrapPanic(err error, stack []byte) error {
	return fmt.Errorf("gqlc: recovered from unexpected panic: %w\n\n%s", err, stack)
}

// Run executes the compiler
func (c *CommandLine) Run(args []string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()

			rerr, ok := r.(error)
			if ok {
				err = wrapPanic(rerr, stack)
				return
			}

			err = wrapPanic(fmt.Errorf("%#v", r), stack)
		}
	}()

	c.SetArgs(args[1:])
	return c.Execute()
}
