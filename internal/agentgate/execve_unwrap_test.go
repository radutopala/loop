package agentgate

import (
	"strings"
)

// unwrapCase is one wrapper invocation. argv may use "@/" for the stub
// directory (needed once env -i drops PATH) and "@ld" for the dynamic loader;
// resolveUnwrapArgs fills them in. A nil want means the wrapper stays the
// command.
type unwrapCase struct {
	name string
	argv []string
	want []string
	// runs marks a kept wrapper that still runs a command we chose not to
	// name (a shell, a quoted env -S string).
	runs bool
	// fails marks a named command the real wrapper never runs, because it
	// rejects a value unwrapCommand doesn't validate.
	fails bool
	// noRun keeps the row out of the differential test: it needs a
	// privilege, a terminal or a sudo the test can't count on.
	noRun bool
}

// resolveUnwrapArgs fills in the "@/" and "@ld" placeholders.
func resolveUnwrapArgs(args []string, dir, loader string) []string {
	if args == nil {
		return nil
	}
	out := make([]string, len(args))
	for i, a := range args {
		if a == "@ld" {
			a = loader
		}
		out[i] = strings.ReplaceAll(a, "@/", dir+"/")
	}
	return out
}

func unwrapCases() []unwrapCase {
	p := "payloadcmd"
	return []unwrapCase{
		{name: "not a wrapper", argv: []string{"/bin/ls", "-al"}, want: []string{"/bin/ls", "-al"}},
		{name: "script runs a shell", argv: []string{"script", "-qc", p + " a", "/dev/null"}, runs: true},

		// env
		{name: "env assignments", argv: []string{"env", "FOO=bar", "HOME=/tmp", p, "-rf", "/data"}, want: []string{p, "-rf", "/data"}},
		{name: "env any = is an assignment", argv: []string{"env", "=x", "a/b=c", "1X=1", p}, want: []string{p}},
		{name: "env -u value", argv: []string{"env", "-u", "X", p, "a"}, want: []string{p, "a"}},
		{name: "env -u attached", argv: []string{"env", "-uX", p}, want: []string{p}},
		{name: "env --unset=", argv: []string{"env", "--unset=X", p}, want: []string{p}},
		{name: "env long prefix", argv: []string{"env", "--un", "X", p}, want: []string{p}},
		{name: "env ambiguous prefix", argv: []string{"env", "--i", "@/" + p}},
		{name: "env -i", argv: []string{"env", "-i", "@/" + p}, want: []string{"@/" + p}},
		{name: "env lone dash", argv: []string{"env", "-", "@/" + p, "a"}, want: []string{"@/" + p, "a"}},
		{name: "env -C -a", argv: []string{"env", "-C", "/tmp", "-a", "foo", p, "x"}, want: []string{p, "x"}},
		{name: "env optional signal", argv: []string{"env", "--block-signal", p}, want: []string{p}},
		{name: "env attached signal", argv: []string{"env", "--block-signal=INT", p}, want: []string{p}},
		{name: "env signal isn't a separate value", argv: []string{"env", "--block-signal", "INT", p}, want: []string{"INT", p}},
		{name: "env options end at assignments", argv: []string{"env", "FOO=1", "-u", "X", p}, want: []string{"-u", "X", p}},
		{name: "env double dash", argv: []string{"env", "--", p}, want: []string{p}},
		{name: "env flag cluster", argv: []string{"env", "-vu", "X", p}, want: []string{p}},
		{name: "env -S", argv: []string{"env", "-S", p + " a  b", "c"}, want: []string{p, "a", "b", "c"}},
		{name: "env -S options", argv: []string{"env", "-S-u X " + p, "c"}, want: []string{p, "c"}},
		{name: "env -S in a cluster", argv: []string{"env", "-vS", p + " a"}, want: []string{p, "a"}},
		{name: "env --split-string blanks", argv: []string{"env", "--split-string=" + p + "\ta\nb"}, want: []string{p, "a", "b"}},
		{name: "env -S empty", argv: []string{"env", "-S", "", p}, want: []string{p}},
		{name: "env -S twice", argv: []string{"env", "-S", "-S " + p, "a"}, want: []string{p, "a"}},
		{name: "env -S quotes refused", argv: []string{"env", "-S", p + " 'a b'"}, runs: true},
		{name: "env -S expansion refused", argv: []string{"env", "-S", p + " ${HOME}"}, runs: true},
		{name: "env -S comment refused", argv: []string{"env", "-S", p + " #c"}, runs: true},
		{name: "env -S without value", argv: []string{"env", "-S"}},
		{name: "env -0 refuses a command", argv: []string{"env", "-0", p}},
		{name: "env help", argv: []string{"env", "--help"}},
		{name: "env unknown long", argv: []string{"env", "--nope", p}},
		{name: "env unknown short", argv: []string{"env", "-q", p}},
		{name: "env flag given a value", argv: []string{"env", "--ignore-environment=1", p}},
		{name: "env without command", argv: []string{"env", "FOO=bar"}},

		// sudo
		{name: "sudo -u value", argv: []string{"sudo", "-u", "root", "rm", "-rf", "/x"}, want: []string{"rm", "-rf", "/x"}, noRun: true},
		{name: "sudo -E -u", argv: []string{"sudo", "-E", "-u", "nobody", "/bin/rm"}, want: []string{"/bin/rm"}, noRun: true},
		{name: "sudo -u attached", argv: []string{"sudo", "-uroot", "rm"}, want: []string{"rm"}, noRun: true},
		{name: "sudo --user=", argv: []string{"sudo", "--user=root", "rm"}, want: []string{"rm"}, noRun: true},
		{name: "sudo value options", argv: []string{"sudo", "-g", "wheel", "-C", "3", "-D", "/tmp", "-p", "", "-T", "10", "-U", "bob", "rm"}, want: []string{"rm"}, noRun: true},
		{name: "sudo -h host", argv: []string{"sudo", "-h", "host", "rm"}, want: []string{"rm"}, noRun: true},
		{name: "sudo --host", argv: []string{"sudo", "--host", "h", "rm"}, want: []string{"rm"}, noRun: true},
		{name: "sudo -h is help", argv: []string{"sudo", "-h"}, noRun: true},
		{name: "sudo -h before an option is help", argv: []string{"sudo", "-h", "-u", "root", "rm"}, noRun: true},
		{name: "sudo -h before an assignment is help", argv: []string{"sudo", "-h", "A=1", "rm"}, noRun: true},
		{name: "sudo assignments anywhere", argv: []string{"sudo", "FOO=1", "-u", "root", "BAR=2", "rm"}, want: []string{"rm"}, noRun: true},
		{name: "sudo path with = is the command", argv: []string{"sudo", "/x=1", "rm"}, want: []string{"/x=1", "rm"}, noRun: true},
		{name: "sudo assignment after --", argv: []string{"sudo", "--", "FOO=1", "rm"}, want: []string{"FOO=1", "rm"}, noRun: true},
		{name: "sudo double dash", argv: []string{"sudo", "--", "-weird-cmd", "arg"}, want: []string{"-weird-cmd", "arg"}, noRun: true},
		{name: "sudo -k with command", argv: []string{"sudo", "-nk", "rm"}, want: []string{"rm"}, noRun: true},
		{name: "sudo --preserve-env", argv: []string{"sudo", "--preserve-env", "rm"}, want: []string{"rm"}, noRun: true},
		{name: "sudo --preserve-env=", argv: []string{"sudo", "--preserve-env=PATH", "rm"}, want: []string{"rm"}, noRun: true},
		{name: "sudo -s shell", argv: []string{"sudo", "-s", "rm"}, noRun: true},
		{name: "sudo -i login shell", argv: []string{"sudo", "-i", "rm"}, noRun: true},
		{name: "sudo -e edit", argv: []string{"sudo", "-e", "/etc/hosts"}, noRun: true},
		{name: "sudo -l list", argv: []string{"sudo", "-l", "rm"}, noRun: true},
		{name: "sudo --shell", argv: []string{"sudo", "--shell", "rm"}, noRun: true},
		{name: "sudo -u without value", argv: []string{"sudo", "-u"}, noRun: true},
		{name: "sudo unknown option", argv: []string{"sudo", "-x", "rm"}, noRun: true},
		{name: "sudo env chain", argv: []string{"sudo", "env", "FOO=bar", "/bin/rm", "-rf"}, want: []string{"/bin/rm", "-rf"}, noRun: true},

		// nice
		{name: "nice -n value", argv: []string{"nice", "-n", "5", p, "a"}, want: []string{p, "a"}},
		{name: "nice -n attached", argv: []string{"nice", "-n5", p}, want: []string{p}},
		{name: "nice -n negative", argv: []string{"nice", "-n", "-5", p}, want: []string{p}},
		{name: "nice --adjustment=", argv: []string{"nice", "--adjustment=5", p}, want: []string{p}},
		{name: "nice long prefix", argv: []string{"nice", "--adj", "5", p}, want: []string{p}},
		{name: "nice -N", argv: []string{"nice", "-5", p}, want: []string{p}},
		{name: "nice --N", argv: []string{"nice", "--5", p}, want: []string{p}},
		{name: "nice -+N", argv: []string{"nice", "-+5", p}, want: []string{p}},
		{name: "nice -N then -n", argv: []string{"nice", "-5", "-n", "3", p}, want: []string{p}},
		{name: "nice double dash", argv: []string{"nice", "--", p}, want: []string{p}},
		{name: "nice options end at the command", argv: []string{"nice", p, "-n", "5"}, want: []string{p, "-n", "5"}},
		{name: "nice lone dash is the command", argv: []string{"nice", "-", p}, want: []string{"-", p}},
		{name: "nice bad adjustment", argv: []string{"nice", "-1x", p}, want: []string{p}, fails: true},
		{name: "nice alone", argv: []string{"nice"}},
		{name: "nice -n without value", argv: []string{"nice", "-n"}},
		{name: "nice empty argument", argv: []string{"nice", "", p}, want: []string{"", p}, noRun: true},

		// timeout
		{name: "timeout duration", argv: []string{"timeout", "5", p, "-s", "KILL"}, want: []string{p, "-s", "KILL"}},
		{name: "timeout -s value", argv: []string{"timeout", "-s", "KILL", "5", p}, want: []string{p}},
		{name: "timeout -s attached", argv: []string{"timeout", "-sKILL", "5", p}, want: []string{p}},
		{name: "timeout --signal=", argv: []string{"timeout", "--signal=KILL", "5", p}, want: []string{p}},
		{name: "timeout long prefix", argv: []string{"timeout", "--s=KILL", "5", p}, want: []string{p}},
		{name: "timeout -k", argv: []string{"timeout", "-k", "1", "5", p}, want: []string{p}},
		{name: "timeout --kill-after", argv: []string{"timeout", "--kill-after", "1", "5", p}, want: []string{p}},
		{name: "timeout flag cluster", argv: []string{"timeout", "-fpv", "5", p}, want: []string{p}},
		{name: "timeout cluster ending in a value", argv: []string{"timeout", "-pk1", "5", p}, want: []string{p}},
		{name: "timeout double dash", argv: []string{"timeout", "--", "5", p}, want: []string{p}},
		{name: "timeout dash dash after duration", argv: []string{"timeout", "5", "--", p}, want: []string{"--", p}},
		{name: "timeout ambiguous prefix", argv: []string{"timeout", "--v", "5", p}},
		{name: "timeout duration only", argv: []string{"timeout", "5"}},
		{name: "timeout help", argv: []string{"timeout", "--help"}},
		{name: "timeout unknown option", argv: []string{"timeout", "-x", "5", p}},
		{name: "timeout nice env chain", argv: []string{"timeout", "5", "nice", "-n", "3", "env", "-u", "X", p, "a"}, want: []string{p, "a"}},

		// nohup
		{name: "nohup", argv: []string{"nohup", p, "--help"}, want: []string{p, "--help"}},
		{name: "nohup double dash", argv: []string{"nohup", "--", p}, want: []string{p}},
		{name: "nohup help", argv: []string{"nohup", "--help"}},
		{name: "nohup unknown option", argv: []string{"nohup", "-x", p}},
		{name: "nohup inner wrapper kept", argv: []string{"nohup", "timeout", "--help"}, want: []string{"timeout", "--help"}},

		// ionice
		{name: "ionice -c value", argv: []string{"ionice", "-c", "3", p}, want: []string{p}},
		{name: "ionice -c attached", argv: []string{"ionice", "-c3", p}, want: []string{p}},
		{name: "ionice --class=", argv: []string{"ionice", "--class=3", p}, want: []string{p}},
		{name: "ionice -c -n", argv: []string{"ionice", "-c", "2", "-n", "3", p}, want: []string{p}},
		{name: "ionice -t", argv: []string{"ionice", "-t", p}, want: []string{p}},
		{name: "ionice options end at the command", argv: []string{"ionice", p, "-c", "3"}, want: []string{p, "-c", "3"}},
		{name: "ionice -p", argv: []string{"ionice", "-p", "1"}},
		{name: "ionice -P", argv: []string{"ionice", "-c", "3", "-P", "1", p}},
		{name: "ionice ambiguous prefix", argv: []string{"ionice", "--cl", "3", p}},
		{name: "ionice alone", argv: []string{"ionice"}},

		// chrt
		{name: "chrt policy priority", argv: []string{"chrt", "-o", "0", p, "a"}, want: []string{p, "a"}},
		{name: "chrt long policy", argv: []string{"chrt", "--other", "0", p}, want: []string{p}},
		{name: "chrt long prefix", argv: []string{"chrt", "--oth", "0", p}, want: []string{p}},
		{name: "chrt batch", argv: []string{"chrt", "-b", "0", p}, want: []string{p}},
		{name: "chrt idle verbose", argv: []string{"chrt", "-v", "-i", "0", p}, want: []string{p}},
		{name: "chrt -R -T", argv: []string{"chrt", "-R", "-T", "5", "-o", "0", p}, want: []string{p}},
		{name: "chrt options end at priority", argv: []string{"chrt", "-o", "0", p, "-p"}, want: []string{p, "-p"}},
		{name: "chrt double dash before priority", argv: []string{"chrt", "-o", "--", "0", p}, want: []string{p}},
		{name: "chrt double dash after priority", argv: []string{"chrt", "-o", "0", "--", p}, want: []string{p}},
		{name: "chrt one double dash after priority", argv: []string{"chrt", "-o", "0", "--", "--", p}, want: []string{"--", p}},
		{name: "chrt default policy", argv: []string{"chrt", "1", p}, want: []string{p}, noRun: true},
		{name: "chrt priority is required", argv: []string{"chrt", "-o", p, "a"}, want: []string{"a"}, fails: true},
		{name: "chrt -p", argv: []string{"chrt", "-p", "1"}},
		{name: "chrt -m", argv: []string{"chrt", "-m"}},
		{name: "chrt priority isn't attached", argv: []string{"chrt", "-o0", p}},
		{name: "chrt without command", argv: []string{"chrt", "-o", "0"}},

		// taskset
		{name: "taskset mask", argv: []string{"taskset", "1", p}, want: []string{p}},
		{name: "taskset -c list", argv: []string{"taskset", "-c", "0", p}, want: []string{p}},
		{name: "taskset --cpu-list prefix", argv: []string{"taskset", "--cpu", "0", p}, want: []string{p}},
		{name: "taskset -ac", argv: []string{"taskset", "-ac", "0", p}, want: []string{p}},
		{name: "taskset options end at mask", argv: []string{"taskset", "1", p, "-p"}, want: []string{p, "-p"}},
		{name: "taskset no double dash after mask", argv: []string{"taskset", "1", "--", p}, want: []string{"--", p}},
		{name: "taskset -p", argv: []string{"taskset", "-p", "1"}},
		{name: "taskset -pc", argv: []string{"taskset", "-pc", "0", "1"}},
		{name: "taskset -c takes no value", argv: []string{"taskset", "-c0", p}},
		{name: "taskset mask only", argv: []string{"taskset", "1"}},

		// stdbuf
		{name: "stdbuf -o attached", argv: []string{"stdbuf", "-oL", p}, want: []string{p}},
		{name: "stdbuf -o value", argv: []string{"stdbuf", "-o", "L", p}, want: []string{p}},
		{name: "stdbuf --output=", argv: []string{"stdbuf", "--output=L", p}, want: []string{p}},
		{name: "stdbuf long prefix", argv: []string{"stdbuf", "--out", "L", p}, want: []string{p}},
		{name: "stdbuf all streams", argv: []string{"stdbuf", "-i0", "-o0", "-eL", p}, want: []string{p}},
		{name: "stdbuf options end at the command", argv: []string{"stdbuf", "-oL", p, "-o"}, want: []string{p, "-o"}},
		{name: "stdbuf needs an option", argv: []string{"stdbuf", p}},
		{name: "stdbuf double dash isn't an option", argv: []string{"stdbuf", "--", p}},
		{name: "stdbuf -o without value", argv: []string{"stdbuf", "-o"}},

		// setsid
		{name: "setsid", argv: []string{"setsid", p, "x"}, want: []string{p, "x"}},
		{name: "setsid -w", argv: []string{"setsid", "-w", p}, want: []string{p}},
		{name: "setsid --fork --wait", argv: []string{"setsid", "--fork", "--wait", p}, want: []string{p}},
		{name: "setsid options end at the command", argv: []string{"setsid", p, "-w"}, want: []string{p, "-w"}},
		{name: "setsid double dash", argv: []string{"setsid", "--", p}, want: []string{p}},
		{name: "setsid -c needs a terminal", argv: []string{"setsid", "-c", p}, want: []string{p}, noRun: true},
		{name: "setsid help", argv: []string{"setsid", "-h"}},
		{name: "setsid alone", argv: []string{"setsid"}},

		// unshare
		{name: "unshare -r", argv: []string{"unshare", "-r", p}, want: []string{p}, noRun: true},
		{name: "unshare --mount=", argv: []string{"unshare", "--mount=/x", p}, want: []string{p}, noRun: true},
		{name: "unshare --mount takes no separate value", argv: []string{"unshare", "--mount", p}, want: []string{p}, noRun: true},
		{name: "unshare -w", argv: []string{"unshare", "-w", "/tmp", p}, want: []string{p}, noRun: true},
		{name: "unshare --wd=", argv: []string{"unshare", "--wd=/tmp", p}, want: []string{p}, noRun: true},
		{name: "unshare -R -S -G", argv: []string{"unshare", "-R", "/", "-S", "0", "-G", "0", p}, want: []string{p}, noRun: true},
		{name: "unshare --map-user", argv: []string{"unshare", "--map-user", "0", p}, want: []string{p}, noRun: true},
		{name: "unshare --propagation", argv: []string{"unshare", "--propagation", "private", p}, want: []string{p}, noRun: true},
		{name: "unshare --kill-child", argv: []string{"unshare", "--kill-child", p}, want: []string{p}, noRun: true},
		{name: "unshare --kill-child=", argv: []string{"unshare", "--kill-child=TERM", p}, want: []string{p}, noRun: true},
		{name: "unshare ambiguous prefix", argv: []string{"unshare", "--map-u", "0", p}},
		{name: "unshare short namespace takes no value", argv: []string{"unshare", "-mfoo", p}},
		{name: "unshare runs a shell", argv: []string{"unshare"}, noRun: true},

		// ld.so
		{name: "loader --library-path", argv: []string{"@ld", "--library-path", "/x", "/usr/bin/env", p, "a"}, want: []string{p, "a"}},
		{name: "loader --argv0", argv: []string{"@ld", "--argv0", "foo", "/usr/bin/env", p}, want: []string{p}},
		{name: "loader flag and double dash", argv: []string{"@ld", "--inhibit-cache", "--", "/usr/bin/env", p}, want: []string{p}},
		{name: "loader program only", argv: []string{"@ld", "/usr/bin/env", p}, want: []string{p}},
		{name: "loader single dash is the program", argv: []string{"@ld", "-x"}, want: []string{"-x"}, fails: true},
		{name: "loader takes no =value", argv: []string{"@ld", "--library-path=/x", "/usr/bin/env", p}},
		{name: "loader takes no prefixes", argv: []string{"@ld", "--lib", "/x", "/usr/bin/env", p}},
		{name: "loader --list", argv: []string{"@ld", "--list", "/usr/bin/env"}},
		{name: "loader value without program", argv: []string{"@ld", "--library-path", "/x"}},
		{name: "loader alone", argv: []string{"@ld"}},
	}
}

// TestUnwrapCommandEmpty covers the argv no table row can hold.
func (s *ExecveSuite) TestUnwrapCommandEmpty() {
	s.Require().Empty(unwrapCommand(nil))
}

func (s *ExecveSuite) TestUnwrapCommand() {
	for _, tc := range unwrapCases() {
		s.Run(tc.name, func() {
			argv := resolveUnwrapArgs(tc.argv, "/stubs", "/lib/ld-linux-x86-64.so.2")
			want := resolveUnwrapArgs(tc.want, "/stubs", "/lib/ld-linux-x86-64.so.2")
			if want == nil {
				want = argv
			}
			s.Require().Equal(want, unwrapCommand(argv))
		})
	}
}

func (s *ExecveSuite) TestWrapperFor() {
	for _, name := range []string{"env", "sudo", "nice", "ionice", "chrt", "timeout", "nohup", "unshare", "setsid", "taskset", "stdbuf", "ld-linux-x86-64.so.2", "ld-linux-aarch64.so.1"} {
		s.Require().NotNilf(wrapperFor(name), "%q should be a wrapper", name)
	}
	for _, name := range []string{"bash", "eatmydata", "script", "."} {
		s.Require().Nilf(wrapperFor(name), "%q shouldn't be a wrapper", name)
	}
}
