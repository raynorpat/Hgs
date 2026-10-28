package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// This test executable supplies native command-shaped fixtures without a shell.
func fakeViewHg(args []string) int {
	if len(args) > 2 && args[0] == "-R" {
		args = args[2:]
	}
	root := filepath.Dir(strings.Split(os.Getenv("HGRCPATH"), string(os.PathListSeparator))[0])
	read := func(name string) string { b, _ := os.ReadFile(filepath.Join(root, name)); return string(b) }
	if len(args) == 0 {
		return 2
	}
	if f, err := os.OpenFile(filepath.Join(root, "calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
		fmt.Fprintln(f, args[0])
		f.Close()
	}
	if f, err := os.OpenFile(filepath.Join(root, "arguments"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
		fmt.Fprintln(f, strings.Join(args, " "))
		f.Close()
	}
	switch args[0] {
	case "version":
		fmt.Print(`[{"ver":"7.2.4"}]`)
	case "help":
		fmt.Print(`"pretxnopen" "pretxnclose" "pretxnclose-bookmark" "pretxnclose-phase" "prepushkey"`)
	case "init":
		return boolExit(os.MkdirAll(filepath.Join(args[1], ".hg"), 0700))
	case "branches":
		fmt.Print(read("refs"))
	case "manifest":
		fmt.Print(read("manifest"))
	case "cat":
		if read("cat_failure") != "" {
			fmt.Print(read("content"))
			return 3
		}
		if read("large") != "" {
			chunk := strings.Repeat("x", 32768)
			for i := 0; i < 4096; i++ {
				if _, err := fmt.Print(chunk); err != nil {
					return 1
				}
			}
		} else if content := read("content"); content == "" {
			return 1 // hg cat exits 1 when no file matched
		} else {
			fmt.Print(content)
		}
	case "files":
		fmt.Print(read("filepath") + "\n")
	case "diff":
		fmt.Print(read("patch"))
	case "log":
		all := strings.Join(args, " ")
		switch {
		case strings.Contains(all, "-r null"):
			fmt.Printf(`[{"node":%q}]`, strings.Repeat("0", 40))
		case strings.Contains(all, "bookmark() or tag()"):
		case strings.Contains(all, "{node}"):
			fmt.Print(viewNode)
		default:
			if strings.Contains(all, "sort(") && read("history") != "" {
				fmt.Print(read("history"))
				break
			}
			if read("changeset") != "" {
				fmt.Print(read("changeset"))
				break
			}
			if read("refs") != "" {
				fmt.Printf(`{"node":%q,"rev":1,"p1node":%q,"p2node":%q,"author":"Author <a@example.test>","desc":"<script>alert(1)</script>","branch":"default","date":[1,0]}`+"\n", viewNode, strings.Repeat("0", 40), strings.Repeat("0", 40))
			}
		}
	default:
		return 2
	}
	return 0
}
func boolExit(err error) int {
	if err != nil {
		return 1
	}
	return 0
}
