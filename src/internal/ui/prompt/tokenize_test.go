package prompt

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	spfTestEnvVar1 = "SPF_TEST_ENV_VAR1"
	spfTestEnvVar2 = "SPF_TEST_ENV_VAR2"
	spfTestEnvVar3 = "SPF_TEST_ENV_VAR3"
	spfTestEnvVar4 = "SPF_TEST_ENV_VAR4"
)

var testEnvValues = map[string]string{ //nolint:gochecknoglobals // This is more like a const. Had to use `var` as go doesn't allows const maps
	spfTestEnvVar1: "1",
	spfTestEnvVar2: "hello",
	spfTestEnvVar3: "",
}

func Test_tokenizePromptCommand(t *testing.T) {
	// Just test that we can split as expected
	// Don't try to test shell substitution in this. This is just
	// to test that tokenize function can handle the results of shell
	// substitution as expected

	testdata := []struct {
		name            string
		command         string
		expectedRes     []string
		isErrorExpected bool
	}{
		{
			name:            "Empty String",
			command:         "",
			expectedRes:     []string{},
			isErrorExpected: false,
		},
		{
			name:            "Parenthesis issue",
			command:         "abcd $(xyz",
			expectedRes:     nil,
			isErrorExpected: true,
		},
		{
			name:            "Parenthesis issue - But no dollar",
			command:         "abcd (xyz",
			expectedRes:     []string{"abcd", "(xyz"},
			isErrorExpected: false,
		},
		{
			name:            "Whitespace",
			command:         "    a b  c  ",
			expectedRes:     []string{"a", "b", "c"},
			isErrorExpected: false,
		},
		{
			name:            "Single token",
			command:         "()",
			expectedRes:     []string{"()"},
			isErrorExpected: false,
		},
		{
			name:            "Special characters",
			command:         "() \t\n\t a $5^&*\v\a\n\uF0AC",
			expectedRes:     []string{"()", "a", "$5^&*", "\a", "\uF0AC"},
			isErrorExpected: false,
		},
	}

	for _, tt := range testdata {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tokenizePromptCommand(tt.command, defaultTestCwd)
			assert.Equal(t, tt.expectedRes, res)
			assert.Equal(t, tt.isErrorExpected, err != nil)
		})
	}
}

// Note : resolving shell subsitution is flaky in windows.
// It usually times out, and environment variables sometimes dont work.
func Test_resolveShellSubstitution(t *testing.T) {
	timeout := shellSubTimeoutInTests
	newLineSuffix := "\n"
	noopCommand := "true"
	if runtime.GOOS == "windows" {
		// Substitution is slow in windows
		timeout = 2 * time.Second
		// Windows uses \r\n as new line for echo
		newLineSuffix = "\r\n"
		noopCommand = "cd ."
	}

	testdata := []struct {
		name            string
		command         string
		expectedResult  string
		isErrorExpected bool
		errorToMatch    error
	}{
		// Test with no substitution being performed
		{
			name:            "Empty String",
			command:         "",
			expectedResult:  "",
			isErrorExpected: false,
			errorToMatch:    nil,
		},
		{
			name:            "String without substitution requirement",
			command:         "   a b c $%^ () {} \a\v\t \u0087",
			expectedResult:  "   a b c $%^ () {} \a\v\t \u0087",
			isErrorExpected: false,
			errorToMatch:    nil,
		},
		{
			name:            "Ill formed command 1",
			command:         "abc $(abc",
			expectedResult:  "",
			isErrorExpected: true,
			errorToMatch:    roundBracketMatchError(),
		},
		{
			name:            "Ill formed command 2",
			command:         "abc $(echo abc) syt ${ sdfc ( {)}",
			expectedResult:  "",
			isErrorExpected: true,
			errorToMatch:    curlyBracketMatchError(),
		},

		// Test with substitution being performed
		{
			name:            "Basic substitution",
			command:         "$(echo abc)",
			expectedResult:  "abc" + newLineSuffix,
			isErrorExpected: false,
			errorToMatch:    nil,
		},
		// Might not work on windows ?
		{
			name:            "Command with internal substitution",
			command:         "$(echo $(echo abc))",
			expectedResult:  "abc" + newLineSuffix,
			isErrorExpected: false,
			errorToMatch:    nil,
		},
		{
			name:            "Multiple substitution",
			command:         fmt.Sprintf("$(echo $(echo hi)) ${%s}", spfTestEnvVar2),
			expectedResult:  fmt.Sprintf("hi%s %s", newLineSuffix, testEnvValues[spfTestEnvVar2]),
			isErrorExpected: false,
			errorToMatch:    nil,
		},
		{
			name:            "Non Existing env var",
			command:         fmt.Sprintf("${%s}", spfTestEnvVar4),
			expectedResult:  "",
			isErrorExpected: true,
			errorToMatch:    envVarNotFoundError{varName: spfTestEnvVar4},
		},
		{
			name:            "Shell substitution inside env var substitution",
			command:         "${$(pwd)}",
			expectedResult:  "",
			isErrorExpected: true,
			errorToMatch:    envVarNotFoundError{varName: "$(pwd)"},
		},
		{
			name:            "Empty output",
			command:         "cd abc $(" + noopCommand + ")",
			expectedResult:  "cd abc ",
			isErrorExpected: false,
			errorToMatch:    nil,
		},
	}

	for _, tt := range testdata {
		t.Run(tt.name, func(t *testing.T) {
			result, err := resolveShellSubstitution(timeout, tt.command, defaultTestCwd)

			assert.Equal(t, tt.expectedResult, result)
			if err != nil {
				assert.True(t, tt.isErrorExpected)
				if tt.errorToMatch != nil {
					assert.ErrorIs(t, err, tt.errorToMatch)
				}
			}
		})
	}

	t.Run("Testing shell substitution timeout", func(t *testing.T) {
		result, err := resolveShellSubstitution(timeout, "$(sleep 2)", defaultTestCwd)
		assert.Empty(t, result)
		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func Test_findEndingParenthesis(t *testing.T) {
	testdata := []struct {
		name        string
		value       string
		openIdx     int
		openPar     rune
		closePar    rune
		expectedRes int
	}{
		{
			name:        "Empty String",
			value:       "",
			openIdx:     0,
			openPar:     '(',
			closePar:    ')',
			expectedRes: -1,
		},
		{
			name:        "Invalid input",
			value:       "abc",
			openIdx:     0,
			openPar:     '(',
			closePar:    ')',
			expectedRes: -1,
		},
		{
			name:        "Simple",
			value:       "abc(def)",
			openIdx:     3,
			openPar:     '(',
			closePar:    ')',
			expectedRes: 7,
		},
		{
			name:  "Nesting Example 1",
			value: "abc(d(e{f})gh)",
			//------01234567890123
			openIdx:     3,
			openPar:     '(',
			closePar:    ')',
			expectedRes: 13,
		},
		{
			name:  "Nesting Example 2",
			value: "abc(d(e{f})gh)",
			//------01234567890123
			openIdx:     5,
			openPar:     '(',
			closePar:    ')',
			expectedRes: 10,
		},
		{
			name:  "Nesting Example 2",
			value: "abc(d(e{f(x}))gh)",
			//------01234567890123456
			openIdx:     7,
			openPar:     '{',
			closePar:    '}',
			expectedRes: 11,
		},
		{
			name:  "No Closing Parenthesis 1",
			value: "abc(def}",
			//------012345678901234
			openIdx:     3,
			openPar:     '(',
			closePar:    ')',
			expectedRes: 8,
		},
		{
			name:  "No Closing Parenthesis 2",
			value: "abc((d(e{f})gh)",
			//------012345678901234
			openIdx:     3,
			openPar:     '(',
			closePar:    ')',
			expectedRes: 15,
		},
		{
			name:  "Asymmetric Parenthesis",
			value: "abc((d(e{f}>gh)",
			//------012345678901234
			openIdx:     8,
			openPar:     '{',
			closePar:    '>',
			expectedRes: 11,
		},
	}

	for _, tt := range testdata {
		t.Run(tt.name, func(t *testing.T) {
			res := findEndingBracket([]rune(tt.value), tt.openIdx, tt.openPar, tt.closePar)
			assert.Equal(t, tt.expectedRes, res)
		})
	}
}

func Test_tokenizeWithQuotes(t *testing.T) {
	testdata := []struct {
		name            string
		command         string
		expectedRes     []string
		isErrorExpected bool
	}{
		// Basic cases
		{
			name:            "Empty String",
			command:         "",
			expectedRes:     []string{},
			isErrorExpected: false,
		},
		{
			name:            "Simple tokens",
			command:         "a b c",
			expectedRes:     []string{"a", "b", "c"},
			isErrorExpected: false,
		},
		{
			name:            "Whitespace handling",
			command:         "    a   b   c    ",
			expectedRes:     []string{"a", "b", "c"},
			isErrorExpected: false,
		},
		{
			name:            "Tab and newline handling",
			command:         "a\tb\nc",
			expectedRes:     []string{"a", "b", "c"},
			isErrorExpected: false,
		},
		{
			name:            "Multiple spaces",
			command:         "command    arg",
			expectedRes:     []string{"command", "arg"},
			isErrorExpected: false,
		},

		// Basic quoting
		{
			name:            "Double quotes",
			command:         `"hello world"`,
			expectedRes:     []string{"hello world"},
			isErrorExpected: false,
		},
		{
			name:            "Single quotes",
			command:         `'hello world'`,
			expectedRes:     []string{"hello world"},
			isErrorExpected: false,
		},
		{
			name:            "Mixed quotes and unquoted",
			command:         `command "arg with spaces" normal`,
			expectedRes:     []string{"command", "arg with spaces", "normal"},
			isErrorExpected: false,
		},
		{
			name:            "Leading and trailing quotes",
			command:         `"command" arg "trailing"`,
			expectedRes:     []string{"command", "arg", "trailing"},
			isErrorExpected: false,
		},

		// Empty quotes
		{
			name:            "Empty double quotes",
			command:         `command ""`,
			expectedRes:     []string{"command", ""},
			isErrorExpected: false,
		},
		{
			name:            "Empty single quotes",
			command:         `command ''`,
			expectedRes:     []string{"command", ""},
			isErrorExpected: false,
		},
		{
			name:            "Only empty quotes",
			command:         `""`,
			expectedRes:     []string{""},
			isErrorExpected: false,
		},

		// Nested different quotes
		{
			name:            "Single quotes inside double quotes",
			command:         `"it's working"`,
			expectedRes:     []string{"it's working"},
			isErrorExpected: false,
		},
		{
			name:            "Double quotes inside single quotes",
			command:         `'he said "hello"'`,
			expectedRes:     []string{`he said "hello"`},
			isErrorExpected: false,
		},

		// Escaping
		{
			name:            "Escaped double quote",
			command:         `"escaped \" quote"`,
			expectedRes:     []string{`escaped " quote`},
			isErrorExpected: false,
		},
		{
			name:            "Escaped single quote",
			command:         `'can\'t'`,
			expectedRes:     []string{`can't`},
			isErrorExpected: false,
		},
		{
			name:            "Escaped backslash",
			command:         `"path\\to\\file"`,
			expectedRes:     []string{`path\to\file`},
			isErrorExpected: false,
		},
		{
			name:            "Multiple escaped backslashes",
			command:         `"\\\\"`,
			expectedRes:     []string{`\\`},
			isErrorExpected: false,
		},
		{
			name:            "Escaped characters outside quotes",
			command:         `a\ b c`,
			expectedRes:     []string{`a b`, `c`},
			isErrorExpected: false,
		},

		// Special characters
		{
			name:            "Special characters in quotes",
			command:         `"$HOME" '${USER}' "$(pwd)"`,
			expectedRes:     []string{"$HOME", "${USER}", "$(pwd)"},
			isErrorExpected: false,
		},
		{
			name:            "Unicode in quotes",
			command:         `"こんにちは" '世界'`,
			expectedRes:     []string{"こんにちは", "世界"},
			isErrorExpected: false,
		},

		// Error cases
		{
			name:            "Unmatched double quote",
			command:         `abcd "sdf`,
			expectedRes:     nil,
			isErrorExpected: true,
		},
		{
			name:            "Unmatched single quote",
			command:         `"abcd'`,
			expectedRes:     nil,
			isErrorExpected: true,
		},
		{
			name:            "Unmatched quotes mixed",
			command:         `abc "def' ghi`,
			expectedRes:     nil,
			isErrorExpected: true,
		},
		{
			name:            "Trailing escape",
			command:         `abc\`,
			expectedRes:     nil,
			isErrorExpected: true,
		},
		{
			name:            "Escape at end of quoted string",
			command:         `"abc\`,
			expectedRes:     nil,
			isErrorExpected: true,
		},

		// Complex cases
		{
			name:            "Multiple quoted sections",
			command:         `"first part" "second part" third`,
			expectedRes:     []string{"first part", "second part", "third"},
			isErrorExpected: false,
		},
		{
			name:            "Quotes with no spaces",
			command:         `"hello""world"`,
			expectedRes:     []string{"hello", "world"},
			isErrorExpected: false,
		},
		{
			name:            "Mixed quotes no spaces",
			command:         `"hello"'world'`,
			expectedRes:     []string{"hello", "world"},
			isErrorExpected: false,
		},

		// Invalid escape sequences (should preserve backslash)
		{
			name:            "Invalid escape sequence \\n",
			command:         `"hello\nworld"`,
			expectedRes:     []string{`hello\nworld`},
			isErrorExpected: false,
		},
		{
			name:            "Invalid escape sequence \\t",
			command:         `"hello\tworld"`,
			expectedRes:     []string{`hello\tworld`},
			isErrorExpected: false,
		},
		{
			name:            "Invalid escape sequence \\x",
			command:         `"hello\xworld"`,
			expectedRes:     []string{`hello\xworld`},
			isErrorExpected: false,
		},
		{
			name:            "Invalid escape sequence \\$",
			command:         `"hello\$world"`,
			expectedRes:     []string{`hello\$world`},
			isErrorExpected: false,
		},
	}

	for _, tt := range testdata {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tokenizeWithQuotes(tt.command)
			assert.Equal(t, tt.expectedRes, res)
			assert.Equal(t, tt.isErrorExpected, err != nil)
		})
	}
}
