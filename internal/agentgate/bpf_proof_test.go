//go:build linux

package agentgate

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"
)

// SeccompProofSuite proves properties of the seccomp program buildFilter
// emits for this GOARCH with the z3 SMT solver, over every syscall number and
// audit arch: the program becomes one SMT-LIB bit-vector term (the verdict
// for a symbolic nr and arch), and each property is proved by z3 finding no
// input that breaks it. `make prove-seccomp` runs it for amd64 and arm64.
//
// The trusted base is the cBPF semantics in smtVerdict, the property
// statements, z3, and the x32 dispatch model in x32Numbers. P4 checks the
// filter against the gate's own trap list; it doesn't claim that list covers
// every way to reach a file or a socket.
//
// Skipped when z3 isn't on PATH, unless SECCOMP_PROOF is set.
type SeccompProofSuite struct {
	suite.Suite
	z3 string
}

func TestSeccompProofSuite(t *testing.T) {
	suite.Run(t, new(SeccompProofSuite))
}

func (s *SeccompProofSuite) SetupSuite() {
	z3, err := exec.LookPath("z3")
	if err != nil {
		if os.Getenv("SECCOMP_PROOF") != "" {
			s.T().Fatal("z3 not on PATH")
		}
		s.T().Skip("z3 not installed; run make prove-seccomp")
	}
	s.z3 = z3
}

// x32Numbers models arch/x86/entry/syscalls/syscall_64.tbl for the trapped
// syscalls: with CONFIG_X86_X32_ABI, nr|x32SyscallBit is dispatched through
// the x32 table, where "common" entries keep their native number and
// execve/execveat are x32-specific.
func x32Numbers(trap []uint32) []uint32 {
	x32Only := map[string]uint32{"execve": 520, "execveat": 545}
	out := make([]uint32, 0, len(trap))
	for _, nr := range trap {
		n, ok := x32Only[syscallNameByNR[int32(nr)]]
		if !ok {
			n = nr
		}
		out = append(out, x32SyscallBit|n)
	}
	return out
}

func (s *SeccompProofSuite) TestFilterProperties() {
	arch, err := auditArchFor(runtime.GOARCH)
	s.Require().NoError(err)
	trap, deny := TrapSyscalls(), DenySyscalls()
	verdict, err := smtVerdict(buildFilter(arch, trap, deny))
	s.Require().NoError(err)

	x86 := arch == auditArchX86_64
	native := fmt.Sprintf("(= arch %s)", bv(arch))
	trapped, denied := smtMember("nr", trap), smtMember("nr", deny)
	isX32 := fmt.Sprintf("(bvuge nr %s)", bv(x32SyscallBit))
	fallback := bv(seccompRetAllow)
	if x86 {
		fallback = fmt.Sprintf("(ite %s %s %s)", isX32, bv(seccompRetErrno|uint32(unix.ENOSYS)), fallback)
	}
	spec := fmt.Sprintf("(ite %s %s (ite %s %s %s))", trapped, bv(seccompRetUserNotif), denied, bv(seccompRetErrno|uint32(unix.EPERM)), fallback)
	stopped := fmt.Sprintf("(or (= res %s) (= res %s))", bv(seccompRetUserNotif), bv(seccompRetErrno|uint32(unix.ENOSYS)))

	props := []struct {
		name, claim string
		x86Only     bool
	}{
		{"P1 wrong audit arch is always KILL_PROCESS", fmt.Sprintf("(or %s (= res %s))", native, bv(seccompRetKillProcess)), false},
		{"P2 native arch: verdict equals the trap/deny spec for every nr", fmt.Sprintf("(or (not %s) (= res %s))", native, spec), false},
		{"P2' native arch: no x32-ABI nr is ALLOWed", fmt.Sprintf("(or (not %s) (not %s) (not (= res %s)))", native, isX32, bv(seccompRetAllow)), true},
		{"P3 no nr is both trapped and denied", fmt.Sprintf("(not (and %s %s))", trapped, denied), false},
		{"P4 every gated dispatch is stopped (x32 ABI off)", fmt.Sprintf("(or (not %s) (not %s) %s)", native, trapped, stopped), true},
		{"P4 every gated dispatch is stopped (x32 ABI on)", fmt.Sprintf("(or (not %s) (not (or %s %s)) %s)", native, trapped, smtMember("nr", x32Numbers(trap)), stopped), true},
	}
	for _, p := range props {
		if p.x86Only && !x86 {
			continue
		}
		s.Run(p.name, func() {
			proved, model := s.prove(verdict, p.claim)
			s.Require().True(proved, "refuted, counterexample %s", model)
		})
	}
}

// The proof must be able to fail: without the x32 guard an x32 number is
// allowed, and with a trap entry dropped the program no longer matches the
// spec. Without these, an encoding that made every claim hold would pass.
func (s *SeccompProofSuite) TestFilterPropertiesCanFail() {
	trap, deny := []uint32{42, 257}, []uint32{425}
	full := buildFilter(auditArchX86_64, trap, deny)
	native := fmt.Sprintf("(= arch %s)", bv(auditArchX86_64))

	noGuard := append(append([]unix.SockFilter{}, full[:len(full)-3]...), full[len(full)-1])
	verdict, err := smtVerdict(noGuard)
	s.Require().NoError(err)
	proved, model := s.prove(verdict, fmt.Sprintf("(or (not %s) (not (bvuge nr %s)) (not (= res %s)))", native, bv(x32SyscallBit), bv(seccompRetAllow)))
	s.Require().False(proved)
	nr, err := strconv.ParseUint(regexp.MustCompile(`\(nr #x([0-9a-f]{8})\)`).FindStringSubmatch(model)[1], 16, 32)
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(uint32(nr), x32SyscallBit)

	verdict, err = smtVerdict(buildFilter(auditArchX86_64, trap[:1], deny))
	s.Require().NoError(err)
	proved, _ = s.prove(verdict, fmt.Sprintf("(or (not %s) (not %s) (= res %s))", native, smtMember("nr", trap), bv(seccompRetUserNotif)))
	s.Require().False(proved)
}

func (s *SeccompProofSuite) TestSmtVerdictRejectsUnsupportedPrograms() {
	for _, tc := range []struct {
		name string
		prog []unix.SockFilter
	}{
		{"empty", nil},
		{"unsupported opcode", []unix.SockFilter{{Code: uint16(unix.BPF_ALU | unix.BPF_ADD | unix.BPF_K)}, {Code: retK}}},
		{"load outside seccomp_data nr/arch", []unix.SockFilter{{Code: ldW, K: 16}, {Code: retK}}},
		{"jump past the end", []unix.SockFilter{{Code: jeqK, Jt: 0, Jf: 5}, {Code: retK}}},
		{"no final RET", []unix.SockFilter{{Code: ldW, K: sdOffsetNR}}},
	} {
		s.Run(tc.name, func() {
			_, err := smtVerdict(tc.prog)
			s.Require().Error(err)
		})
	}
}

// prove reports whether claim holds for every nr and arch, given verdict as
// the filter's result res; when it doesn't, model is z3's counterexample.
func (s *SeccompProofSuite) prove(verdict, claim string) (proved bool, model string) {
	script := "(set-logic QF_BV)\n" +
		"(declare-const nr (_ BitVec 32))\n" +
		"(declare-const arch (_ BitVec 32))\n" +
		"(define-fun res () (_ BitVec 32) " + verdict + ")\n" +
		"(assert (not " + claim + "))\n" +
		"(check-sat)\n"
	switch out := s.z3Run(script); out {
	case "unsat":
		return true, ""
	case "sat":
		return false, s.z3Run(script + "(get-value (nr arch res))\n")
	default:
		s.FailNow("unexpected z3 output", out)
		return false, ""
	}
}

func (s *SeccompProofSuite) z3Run(script string) string {
	cmd := exec.Command(s.z3, "-in", "-smt2")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, "z3: %s", out)
	text := strings.TrimSpace(string(out))
	if verdict, rest, ok := strings.Cut(text, "\n"); ok && verdict == "sat" {
		return strings.Join(strings.Fields(rest), " ")
	}
	return text
}

// smtVerdict renders a seccomp program as one SMT-LIB term over the symbolic
// nr and arch: the value its RET returns. It accepts only the instructions
// buildFilter emits (LD|W|ABS of nr or arch, JEQ|K, JGE|K, RET|K) and checks
// what the kernel's filter checker would: a non-empty program of at most 4096
// instructions, jumps in range, a RET last. Jumps only go forward, so the
// recursion follows each path once.
func smtVerdict(prog []unix.SockFilter) (string, error) {
	if len(prog) == 0 || len(prog) > 4096 {
		return "", fmt.Errorf("program length %d", len(prog))
	}
	if prog[len(prog)-1].Code != retK {
		return "", fmt.Errorf("last instruction is not RET")
	}
	var run func(pc int, a string) (string, error)
	run = func(pc int, a string) (string, error) {
		in := prog[pc]
		switch in.Code {
		case retK:
			return bv(in.K), nil
		case ldW:
			switch in.K {
			case sdOffsetNR:
				return run(pc+1, "nr")
			case sdOffsetArch:
				return run(pc+1, "arch")
			}
			return "", fmt.Errorf("load of offset %d at %d", in.K, pc)
		case jeqK, jgeK:
			jt, jf := pc+1+int(in.Jt), pc+1+int(in.Jf)
			if jt >= len(prog) || jf >= len(prog) {
				return "", fmt.Errorf("jump out of range at %d", pc)
			}
			t, err := run(jt, a)
			if err != nil {
				return "", err
			}
			f, err := run(jf, a)
			if err != nil {
				return "", err
			}
			op := "="
			if in.Code == jgeK {
				op = "bvuge" // unsigned, as in the kernel
			}
			return fmt.Sprintf("(ite (%s %s %s) %s %s)", op, a, bv(in.K), t, f), nil
		}
		return "", fmt.Errorf("unsupported opcode %#x at %d", in.Code, pc)
	}
	return run(0, bv(0))
}

func smtMember(v string, xs []uint32) string {
	if len(xs) == 0 {
		return "false"
	}
	terms := make([]string, len(xs))
	for i, x := range xs {
		terms[i] = fmt.Sprintf("(= %s %s)", v, bv(x))
	}
	return "(or false " + strings.Join(terms, " ") + ")"
}

func bv(k uint32) string {
	return fmt.Sprintf("#x%08x", k)
}
