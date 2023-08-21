package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	// This initializes gpython for runtime execution and is essential.
	// It defines forward-declared symbols and registers native built-in modules, such as sys and time.
	_ "github.com/go-python/gpython/stdlib"

	"github.com/go-python/gpython/py"
	"github.com/yetsing/startprompt"
	"github.com/yetsing/startprompt/lexer"
	"github.com/yetsing/startprompt/token"
)

// 全局的 repl 对象
var grepl *Repl

var keywords = []string{
	"False",
	"await",
	"else",
	"import",
	"pass",
	"None",
	"break",
	"except",
	"in",
	"raise",
	"True",
	"class",
	"finally",
	"is",
	"return",
	"and",
	"continue",
	"for",
	"lambda",
	"try",
	"as",
	"def",
	"from",
	"nonlocal",
	"while",
	"assert",
	"del",
	"global",
	"not",
	"with",
	"async",
	"elif",
	"if",
	"or",
	"yield",
}

func isKeyword(name string) bool {
	for _, keyword := range keywords {
		if keyword == name {
			return true
		}
	}
	return false
}

func pyTokens(code string) []token.Token {
	l := lexer.NewPy3Lexer(code)
	tokens := l.Tokens()
	converted := make([]token.Token, len(tokens))
	// 更细致的 token 类型
	for i, t := range tokens {
		if t.TypeIs(token.Name) && isKeyword(t.Literal) {
			nt := token.NewToken(token.Keyword, t.Literal)
			converted[i] = nt
		} else {
			converted[i] = t
		}
	}
	return converted
}

type PythonCode struct {
	document *startprompt.Document
}

func newMultilineCode(document *startprompt.Document) startprompt.Code {
	return &PythonCode{document: document}
}

func (c *PythonCode) GetTokens() []token.Token {
	return pyTokens(c.document.Text())
}

func (c *PythonCode) Complete() string {
	completions := c.GetCompletions()
	if len(completions) == 1 {
		return completions[0].Suffix
	}
	return ""
}

func (c *PythonCode) GetCompletions() []*startprompt.Completion {
	text := c.document.Text()
	head, coms, tail := grepl.Completer(text, c.document.CursorPosition())
	if len(coms) == 0 {
		return nil
	}

	// 计算剩余补全的文本 (Suffix)
	remainLength := len(text) - len(head) - len(tail)
	completions := make([]*startprompt.Completion, len(coms))
	for i, com := range coms {
		completions[i] = &startprompt.Completion{
			Display: com,
			Suffix:  com[remainLength:],
		}
	}
	return completions
}

func (c *PythonCode) ContinueInput() bool {
	text := c.document.Text()
	_, err := py.Compile(text+"\n", grepl.prog, py.SingleMode, 0, true)
	if err != nil {
		// Detect that we should start a continuation line
		errText := err.Error()
		if strings.Contains(errText, "unexpected EOF while parsing") || strings.Contains(errText, "EOF while scanning triple-quoted string literal") {
			stripped := strings.TrimSpace(text)
			isComment := len(stripped) > 0 && stripped[0] == '#'
			return !isComment
		}
	}
	// 用于需要连续按下两次 Enter 才结束当前输入
	return false
}

type Repl struct {
	Context py.Context
	Module  *py.Module
	prog    string
}

func NewRepl(ctx py.Context) *Repl {
	if ctx == nil {
		ctx = py.NewContext(py.DefaultContextOpts())
	}
	r := &Repl{
		Context: ctx,
		prog:    "<stdin>",
	}
	var err error
	r.Module, err = ctx.ModuleInit(&py.ModuleImpl{
		Info: py.ModuleInfo{
			FileDesc: r.prog,
		},
	})
	if err != nil {
		panic(err)
	}
	return r
}

func (r *Repl) Run(line string) {
	code, err := py.Compile(line+"\n", r.prog, py.SingleMode, 0, true)
	if err != nil {
		fmt.Printf("compile error: %v\n", err)
		return
	}
	_, err = r.Context.RunCode(code, r.Module.Globals, r.Module.Globals, nil)
	if err != nil {
		py.TracebackDump(err)
	}
}

// Completer WordCompleter takes the currently edited line with the cursor
// position and returns the completion candidates for the partial word
// to be completed. If the line is "Hello, wo!!!" and the cursor is
// before the first '!', ("Hello, wo!!!", 9) is passed to the
// completer which may returns ("Hello, ", {"world", "Word"}, "!!!")
// to have "Hello, world!!!".
func (r *Repl) Completer(line string, pos int) (head string, completions []string, tail string) {
	head = line[:pos]
	tail = line[pos:]
	lastSpace := strings.LastIndex(head, " ")
	head, partial := line[:lastSpace+1], line[lastSpace+1:]
	// log.Printf("head = %q, partial = %q, tail = %q", head, partial, tail)
	found := make(map[string]struct{})
	match := func(d py.StringDict) {
		for k := range d {
			if strings.HasPrefix(k, partial) {
				if _, ok := found[k]; !ok {
					completions = append(completions, k)
					found[k] = struct{}{}
				}
			}
		}
	}
	match(r.Module.Globals)
	match(r.Context.Store().Builtins.Globals)
	sort.Strings(completions)
	return head, completions, tail
}

func main() {
	c, err := startprompt.NewCommandLine(&startprompt.CommandLineOption{NewCodeFunc: newMultilineCode})
	if err != nil {
		fmt.Printf("failed to startprompt.NewCommandLine: %v\n", err)
		return
	}

	grepl = NewRepl(nil)

	for {
		line, err := c.ReadInput()
		if err != nil {
			if errors.Is(err, startprompt.ExitError) {
				fmt.Printf("\nDo you really want to exit ([y]/n)?")
				reader := bufio.NewReader(os.Stdin)
				reply, err := reader.ReadByte()
				if err != nil {
					fmt.Printf("read error: %v\n", err)
					return
				}
				if reply == 'n' {
					continue
				}
			} else {
				fmt.Printf("ReadInput error: %v\n", err)
			}
			return
		}
		grepl.Run(line)
	}
}
