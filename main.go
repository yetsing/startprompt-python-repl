package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/yetsing/startprompt/terminalcolor"
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

var pyschema = map[token.TokenType]terminalcolor.Style{
	token.Keyword:  terminalcolor.NewFgColorStyleHex("#ee00ee"),
	token.Operator: terminalcolor.NewFgColorStyleHex("#aa6666"),
	token.Number:   terminalcolor.NewFgColorStyleHex("#2aacb8"),
	token.String:   terminalcolor.NewFgColorStyleHex("#6aab73"),

	token.Error:   terminalcolor.NewColorStyleHex("#000000", "#ff8888"),
	token.Comment: terminalcolor.NewFgColorStyleHex("#0000dd"),

	token.CompletionMenuCurrentCompletion: terminalcolor.NewColorStyleHex("#000000", "#dddddd"),
	token.CompletionMenuCompletion:        terminalcolor.NewColorStyleHex("#ffff88", "#888888"),
	token.CompletionProgressButton:        terminalcolor.NewColorStyleHex("", "#000000"),
	token.CompletionProgressBar:           terminalcolor.NewColorStyleHex("", "#aaaaaa"),

	token.Prompt: terminalcolor.NewFgColorStyleHex("#004400"),
}

type Prompt struct {
	line *startprompt.Line
	code startprompt.Code
}

func NewPrompt(line *startprompt.Line, code startprompt.Code) startprompt.Prompt {
	return &Prompt{line: line, code: code}
}

func (p *Prompt) GetPrompt() []token.Token {
	tk := token.NewToken(token.Prompt, fmt.Sprintf("In [%d]: ", grepl.inputCount))
	return []token.Token{tk}
}

func (p *Prompt) GetSecondLinePrefix() []token.Token {
	// 拿到默认提示符宽度
	var sb strings.Builder
	for _, t := range p.GetPrompt() {
		sb.WriteString(t.Literal)
	}
	promptText := sb.String()
	spaces := runewidth.StringWidth(promptText) - 5
	// 输出类似这样的 "...  " ，宽度跟默认提示符一样
	return []token.Token{
		{
			token.PromptSecondLinePrefix,
			repeatByte(' ', spaces),
		},
		{
			token.PromptSecondLinePrefix,
			repeatByte('.', 3) + ": ",
		},
	}
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
	tokens   []token.Token
}

func newMultilineCode(document *startprompt.Document) startprompt.Code {
	return &PythonCode{document: document}
}

func (c *PythonCode) GetTokens() []token.Token {
	if len(c.tokens) == 0 {
		c.tokens = pyTokens(c.document.Text())
	}
	return c.tokens
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

func (c *PythonCode) hasIndent() bool {
	for _, t := range c.GetTokens() {
		if t.TypeIs(token.Indent) {
			return true
		}
	}
	return false
}

func (c *PythonCode) ContinueInput() bool {
	// 光标不在最后一行，直接换行即可
	if !c.document.OnLastLine() {
		return true
	}
	text := c.document.Text()
	if len(text) == 0 {
		return false
	}
	_, err := py.Compile(text+"\n", grepl.prog, py.SingleMode, 0, true)
	if err != nil {
		// 判断是否完整语句，比如 if 2 > 1: 并不是完整的语句，后面还需要有语句
		errText := err.Error()
		if strings.Contains(errText, "unexpected EOF while parsing") || strings.Contains(errText, "EOF while scanning triple-quoted string literal") {
			stripped := strings.TrimSpace(text)
			isComment := len(stripped) > 0 && stripped[0] == '#'
			return !isComment
		}
	}
	// 如果有缩进，需要连按两次 Enter 才结束当前输入
	if c.hasIndent() {
		text = strings.TrimRight(text, " ")
		return !strings.HasSuffix(text, "\n")
	}
	return false
}

// Repl ref: https://github.com/go-python/gpython/blob/main/repl/repl.go
type Repl struct {
	Context py.Context
	Module  *py.Module
	prog    string

	inputCount int
}

func NewRepl(ctx py.Context) *Repl {
	if ctx == nil {
		ctx = py.NewContext(py.DefaultContextOpts())
	}
	r := &Repl{
		Context:    ctx,
		prog:       "<stdin>",
		inputCount: 1,
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
	r.inputCount++
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
	c, err := startprompt.NewCommandLine(&startprompt.CommandLineOption{
		NewCodeFunc:   newMultilineCode,
		NewPromptFunc: NewPrompt,
		Schema:        pyschema,
		AutoIndent:    true,
	})
	if err != nil {
		fmt.Printf("failed to startprompt.NewCommandLine: %v\n", err)
		return
	}

	grepl = NewRepl(nil)

	fmt.Println(`Type "Ctrl-D" to exit.`)
	for {
		line, err := c.ReadInput()
		if err != nil {
			if errors.Is(err, startprompt.ExitError) {
				fmt.Printf("Do you really want to exit ([y]/n)? ")
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
		if len(line) == 0 {
			continue
		}
		grepl.Run(line)
		fmt.Printf("\n")
	}
}
