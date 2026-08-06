package password

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"
)

// policy.go is the whole password policy, and its shortness is the design.
//
// # Why there are no composition rules
//
// "One uppercase, one digit, one symbol" is the rule most products ship and it is measurably counter-productive:
// it converts `correct horse battery staple` into `Password1!`, because the rule is satisfiable by a template
// and people use the template. NIST SP 800-63B withdrew the recommendation for exactly this reason. What
// replaces it is a LENGTH floor plus a check against the passwords attackers actually try — which is what is
// here.
//
// # Why the floor is twelve
//
// Twelve is long enough that the classic short list (`password`, `123456`, `qwerty`) is excluded by length
// alone, which is worth noticing: it means the blocklist below does not need to carry them and can spend its
// entries on the LONG common passwords, which are the ones the floor does not catch.
//
// # 🔴 What this policy does not claim
//
// It is a floor, not a breach oracle. The bundled list is a few hundred entries; a real breach corpus is
// hundreds of millions and lives behind a network call this product does not make (a k-anonymity range query to
// a third party is a request describing our user's password prefix to somebody else, on a page where we have
// just promised the opposite). So the copy says "appears in public breach lists" only where that is true of the
// bundled list, and the limitation is stated here rather than implied to be more than it is.

// Errors the caller renders. Each carries what to do next, because a refusal that only names the failure is a
// dead end wearing a red border.
var (
	ErrTooShort  = fmt.Errorf("use at least %d characters — a short sentence works well", MinLength)
	ErrCommon    = errors.New("that password appears in public breach lists — choose one you have not used elsewhere")
	ErrLikeEmail = errors.New("that password contains your email address — choose something unrelated to it")
	ErrTooLong   = fmt.Errorf("use at most %d characters", MaxLength)
)

// MinLength is the floor. See the file header for why it is not accompanied by composition rules.
const MinLength = 12

// MaxLength bounds the argon2id input.
//
// 🔴 It is a DENIAL-OF-SERVICE bound, not a security rule: argon2id happily hashes a megabyte, and a sign-in
// endpoint that will do so on request is a way to spend the platform's memory from an unauthenticated route. 200
// is far above any real password and far below anything that costs us.
const MaxLength = 200

//go:embed common_passwords.txt
var commonList string

var (
	commonOnce sync.Once
	commonSet  map[string]struct{}
)

func common() map[string]struct{} {
	commonOnce.Do(func() {
		commonSet = make(map[string]struct{}, 512)
		for _, line := range strings.Split(commonList, "\n") {
			line = strings.ToLower(strings.TrimSpace(line))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			commonSet[line] = struct{}{}
		}
	})
	return commonSet
}

// CheckPolicy validates a proposed password against the floor, the blocklist and the person's own address.
//
// `email` may be empty (a change flow that does not know it), in which case that one check is skipped rather
// than silently passed — the difference matters only to a reader, and a reader is who this comment is for.
func CheckPolicy(plaintext, email string) error {
	if len([]rune(plaintext)) < MinLength {
		return ErrTooShort
	}
	if len(plaintext) > MaxLength {
		return ErrTooLong
	}
	lower := strings.ToLower(plaintext)
	if _, bad := common()[lower]; bad {
		return ErrCommon
	}
	if structurallyWeak(lower) {
		// Reported as ErrCommon rather than as its own error. "Your password is a repeated character" is a
		// distinction that helps nobody choose a better one, and one message means one thing to translate.
		return ErrCommon
	}
	if email != "" {
		e := strings.ToLower(strings.TrimSpace(email))
		local := e
		if at := strings.LastIndex(e, "@"); at > 0 {
			local = e[:at]
		}
		// The whole address, or the local part when it is long enough to be meaningful. A three-letter local
		// part like `ops` would otherwise ban every password containing "ops".
		if strings.Contains(lower, e) || (len(local) >= 4 && strings.Contains(lower, local)) {
			return ErrLikeEmail
		}
	}
	return nil
}

// structurallyWeak catches the long-but-trivial passwords a fixed list cannot enumerate: one character
// repeated, a run up or down the number line or the alphabet, and a straight walk along a keyboard row.
//
// These are generated families rather than entries, so a list would need thousands of rows to cover what four
// checks do — and would still miss the next variation.
func structurallyWeak(lower string) bool {
	r := []rune(lower)
	if len(r) == 0 {
		return false
	}
	// One character, repeated.
	same := true
	for _, c := range r[1:] {
		if c != r[0] {
			same = false
			break
		}
	}
	if same {
		return true
	}
	// A monotone run of adjacent code points, up or down: 123456789012, abcdefghijkl, zyxwvutsrqpo.
	if monotoneRun(r, 1) || monotoneRun(r, -1) {
		return true
	}
	// A walk along a keyboard row, in either direction, of the whole password.
	for _, row := range keyboardRows {
		if strings.Contains(row, lower) || strings.Contains(reverse(row), lower) {
			return true
		}
	}
	return false
}

// monotoneRun reports whether every character steps by `step` from the one before it.
//
// Digits WRAP: `123456789012` is the twelve-character password a person types when the field demands twelve
// characters, and it is not a run by code point — '9' is 57 and '0' is 48. Treating the ten digits as a cycle
// is what makes the check catch the password people actually choose rather than the one the arithmetic
// happened to describe. Letters do not wrap, because `zab…` is not a thing anybody types.
func monotoneRun(r []rune, step rune) bool {
	if len(r) < 2 {
		return false
	}
	for i := 1; i < len(r); i++ {
		if !isAlnum(r[i]) || !isAlnum(r[i-1]) {
			return false
		}
		prev, cur := r[i-1], r[i]
		if unicode.IsDigit(prev) && unicode.IsDigit(cur) {
			if (prev-'0'+step+10)%10 != cur-'0' {
				return false
			}
			continue
		}
		if cur != prev+step {
			return false
		}
	}
	return true
}

func isAlnum(c rune) bool { return unicode.IsLetter(c) || unicode.IsDigit(c) }

var keyboardRows = []string{
	"1234567890-=",
	"qwertyuiop[]",
	"asdfghjkl;'",
	"zxcvbnm,./",
	"qwertzuiop", // the other common layout, so `qwertzuiopasdf` is not treated as strong here and weak there
	"azertyuiop",
}

func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
