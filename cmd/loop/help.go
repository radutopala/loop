package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// rootLong heads `loop --help`.
const rootLong = "loop - AI-powered development platform with Claude agents, browser automation, and team collaboration"

// expandArgs splits a colon-joined command path such as review:run into the
// words cobra resolves, wherever it appears, so the paths the help prints run
// as is. Only paths of root's nested commands are split; top-level names that
// carry a colon themselves (app:url) and flag values pass through.
func expandArgs(root *cobra.Command, args []string) []string {
	paths := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.HasParent() && sub.Parent() != root {
				paths[colonPath(sub)] = true
			}
			walk(sub)
		}
	}
	walk(root)
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if paths[arg] {
			out = append(out, strings.Split(arg, ":")...)
			continue
		}
		out = append(out, arg)
	}
	return out
}

// colonPath is the command's path below the root, joined with colons.
func colonPath(c *cobra.Command) string {
	return strings.ReplaceAll(strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" "), " ", ":")
}

// leaves lists every runnable, visible command under c, so a group such as
// review shows as review:run and review:dedup. help and completion stay out.
func leaves(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		if sub.Runnable() {
			out = append(out, sub)
		}
		out = append(out, leaves(sub)...)
	}
	return out
}

// nameHelpFlags gives every command under root a --help flag named by its
// colon path; cobra's own says "help for scan" for quality:scan.
func nameHelpFlags(root *cobra.Command) {
	for _, sub := range root.Commands() {
		sub.InitDefaultHelpFlag()
		sub.Flags().Lookup("help").Usage = "help for " + colonPath(sub)
		nameHelpFlags(sub)
	}
}

// isZeroDefault reports whether a flag's default is its type's zero value,
// which the command list leaves out.
func isZeroDefault(f *pflag.Flag) bool {
	switch f.DefValue {
	case "", "false", "0", "[]", "0s":
		return true
	}
	return false
}

// help prints a command's description, then its usage.
func help(c *cobra.Command, _ []string) {
	desc := c.Long
	if desc == "" {
		desc = c.Short
	}
	if desc != "" {
		_, _ = fmt.Fprintln(c.OutOrStdout(), strings.TrimRight(desc, "\n")+"\n")
	}
	_ = writeUsage(c, c.OutOrStdout())
}

// usage is the usage cobra prints after a command error.
func usage(c *cobra.Command) error {
	return writeUsage(c, c.OutOrStderr())
}

// writeUsage renders a command's usage from the command tree: every runnable
// command beneath it by colon path, with aliases and flags, so a new command
// shows up without anyone editing a list.
func writeUsage(c *cobra.Command, w io.Writer) error {
	var b strings.Builder
	path := c.Root().Name()
	if c.HasParent() {
		path += " " + colonPath(c)
	}
	b.WriteString("Usage:")
	if c.Runnable() {
		b.WriteString("\n  " + path + strings.TrimPrefix(c.UseLine(), c.CommandPath()))
	}
	subs := leaves(c)
	if len(subs) > 0 {
		b.WriteString("\n  " + c.Root().Name() + " [command]\n\nAvailable Commands:")
		for _, sub := range subs {
			line := sub.Short
			switch len(sub.Aliases) {
			case 0:
			case 1:
				line += " (alias: " + sub.Aliases[0] + ")"
			default:
				line += " (aliases: " + strings.Join(sub.Aliases, ", ") + ")"
			}
			fmt.Fprintf(&b, "\n  %-24s %s", colonPath(sub), line)
			sub.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
				if f.Hidden || f.Name == "help" {
					return
				}
				usage := f.Usage
				if !isZeroDefault(f) {
					usage += " [default: " + f.DefValue + "]"
				}
				fmt.Fprintf(&b, "\n    %-22s %s", "--"+f.Name, usage)
			})
		}
	}
	if c.HasParent() && c.HasAvailableLocalFlags() {
		b.WriteString("\n\nFlags:\n" + strings.TrimRight(c.LocalFlags().FlagUsages(), " \n"))
	}
	if c.HasAvailableInheritedFlags() {
		b.WriteString("\n\nGlobal Flags:\n" + strings.TrimRight(c.InheritedFlags().FlagUsages(), " \n"))
	}
	if len(subs) > 0 {
		b.WriteString("\n\nUse \"" + c.Root().Name() + " [command] --help\" for more information about a command.")
	}
	_, err := fmt.Fprintln(w, b.String())
	return err
}
