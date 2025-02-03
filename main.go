package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var editor = os.Getenv("EDITOR")
var zetDir = "./zettel"
var defaultPrefix = "tmp"

// prefixes that are disallowed because they will come in conflict with
// subcommands
var reservedPrefixes = []string{
	"branch",
	"next",
	"previous",
	"resolve",
	"open",
	"help",
}

// handleCompletion provides simple autocompletion.
func handleCompletion(completions ...string) bool {

	// Get the current word being completed
	words := strings.Fields(os.Getenv("COMP_LINE"))

	// If there are no words yet, print all possible completions
	if len(words) == 1 {
		for _, c := range completions {
			fmt.Println(c)
		}
		return false
	}

	lastWord := words[len(words)-1]

	// stop if exact match found
	for _, c := range completions {
		if len(c) != len(lastWord) {
			continue
		}

		if c == lastWord {
			return true // completion has complete match
		}
	}

	for _, c := range completions {
		if strings.HasPrefix(c, lastWord) {
			fmt.Println(c)
		}
	}

	return false
}

func main() {
	log.SetFlags(0) // turn off timestamping log statements, this is a cli app

	err := os.MkdirAll(zetDir, os.ModePerm) // ensures existence of zettel dir
	if err != nil {
		log.Fatalf("Unable to ensure zettel dir '%s': %s", zetDir, err)
	}

	shift(&os.Args)

	// TODO: completion must be some kind of statemachine...
	// // Detect Bash completion request
	// compline := os.Getenv("COMP_LINE")
	// if compline != "" {
	// 	full := handleCompletion(
	// 		"create",
	// 		"branch",
	// 		"resolve",
	// 	)
	// 	if !full {
	// 		return
	// 	}
	// }

	if len(os.Args) == 0 {
		// NOTE: shorthand for create with default prefix
		CreateCommand(defaultPrefix)
		return
	}

	if len(os.Args) == 1 {
		// NOTE: support for shorthand prefixes

		// add checks for supported singular commands here

		// check against reserved stuff
		for _, e := range reservedPrefixes {
			if e == os.Args[0] {
				panic("reserved") //TODO: do this better
			}
		}

		CreateCommand(os.Args[0])
		return
	}

	// TODO: subcommand tree
	switch shift(&os.Args) {
	case "branch":
		if len(os.Args) == 0 {
			panic("TODO: implement usage: need to pass parent id")
		}

		BranchCommand()
	case "resolve":
		if len(os.Args) == 0 {
			panic("TODO: implement usage: need to pass id to resolve")
		}

		ResolveCommand()
	}
}

func BranchCommand() {

	// NOTE: how to make sure that the file names in the system and the links
	// are always in sync?
	//	- normally, zets are write-only, except for renaming and extraction and
	//	possible corruption/deletion
	//	- if the zettel is master, the functions for modification must account
	//	for updating no-longer-valid links
	//	- if the file system is master, the situation is more unknown, and
	//	responsibilities aren't clear
	//	- therefore the zettel should be master

	parentId := os.Args[0]

	fileName := fmt.Sprintf("%s.md", parentId)
	filePath := path.Join(zetDir, fileName)

	// read in the file
	byteContent, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Unable to open parent '%s' for branching: %s", filePath, err)
	}

	content := string(byteContent)
	links := ExtractLinksFromContent(content)
	branches := FilterBranches(links, parentId)
	next, err := NextBranch(branches)
	if err != nil {
		log.Fatalf("Unable to calculate next branch: %s", err)
	}

	branchId := parentId + next

	createZettelFile(branchId + "1") // start branches on sequence no. 1

	// output branch link
	fmt.Printf("[[%s]]\n", branchId)
}

func fileExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return err == nil || !os.IsNotExist(err)
}

func createZettelFile(zettelId string) string {
	fileName := fmt.Sprintf("%s.md", zettelId)
	filePath := path.Join(zetDir, fileName)

	if fileExists(filePath) {
		log.Fatalf("Attempted to create existing file: %s", filePath)
	}

	f, err := os.Create(filePath)
	if err != nil {
		log.Fatalf("Unable to create file '%s': %s", filePath, err)
	}
	ts := timestamp()
	content := fmt.Sprintf("---\nzettel: %s\ndate: %s\n---\n\n\n\n", zettelId, ts)
	f.Write([]byte(content))
	f.Close()
	return filePath
}

// ExtractLinksFromContent takes the entire content of a zettel and extracts
// all links from it, stripping them of markup, leaving only the linked zettel
// IDs as a string slice.
func ExtractLinksFromContent(content string) []string {
	var links []string

	r := regexp.MustCompile(`\[\[(?P<link>[a-zA-Z0-9\.\-\_]+)\]\]`)

	for _, line := range strings.Split(content, "\n") {

		match := r.FindStringSubmatch(line)
		if len(match) < 2 {
			continue
		}

		links = append(links, match[1])

	}

	return links
}

// FilterBranches takes a slice of links (as stripped zettel IDs) and a zettel
// ID, and filters out all links that are not direct branches of the zettel ID.
func FilterBranches(links []string, parentId string) []string {
	var branches []string

	split := strings.Split(parentId, ".")

	// Branches are always alphabetically suffixed. links to specific zettels
	// in a branch have the sequence number
	r := regexp.MustCompile(
		fmt.Sprintf(`%v\.(?P<branch>%v[a-z]+$)`, split[0], split[1]),
	)

	for _, l := range links {
		match := r.FindStringSubmatch(l)
		if len(match) > 1 {
			branch := fmt.Sprintf("%v.%v", split[0], match[1])
			branches = append(branches, branch)
		}
	}

	return branches
}

// NextBranch takes a list of sibling zettel IDs and returns the ID of the next
// upcoming branch on the parent zettel, as well as an error. In the case of an
// empty slice (no other children of current zettel), returns an 'a'.
func NextBranch(branches []string) (string, error) {

	// the first branch will always be 'a' in a numbered sceme
	if len(branches) == 0 {
		return "a", nil
	}

	var err error

	var isDigits bool
	maxChars := ""
	for _, branch := range branches {
		_, branch, isDigits, err = StripLeaf(branch)
		if err != nil {
			return "", fmt.Errorf("unable to strip branch leaf: %w", err)
		}

		// TODO: clean this up
		if isDigits {
			panic("Attempting to process branch that ends in a number. This is no longer applicable, and there must have happened a programming error. This is a bug.")
		} else {
			maxChars, err = AlphaMax(maxChars, branch)
			if err != nil {
				return "", err
			}
		}
	}

	if maxChars == "" {
		panic("highest branch number not detected after iterating through all given branches in NextBranch")
	}

	nextAlphaBranch, err := IncrementAlphaBranch(maxChars)
	if err != nil {
		return "", fmt.Errorf("unable to increment leaf: %w", err)
	}
	return nextAlphaBranch, nil
}

// StripLeaf takes a zettel ID and strips the leaf branch off it, splitting the
// ID into its parent and child components.
//
// E.g: tmp.12.321aa32c69 -> 'tmp.12.321aa32c', '69'
//
// Returns base, branch, a boolean stating if the stripped leaf was numeric or
// not, and potentially an error.
func StripLeaf(id string) (string, string, bool, error) {
	var base string
	var branch string
	var isDigits bool
	var err error

	runes := []rune(id)
	lastRune := runes[len(runes)-1]
	isDigits = unicode.IsDigit(lastRune)

	i := -2
	for unicode.IsDigit(runes[len(runes)+i]) == isDigits && i+len(runes) > 0 {
		i -= 1
	}

	base = id[:len(runes)+i+1]
	branch = id[len(runes)+i+1:]

	return base, branch, isDigits, err
}

// AlphaMax takes two alphabetic strings and returns the one with the highest
// lexical value. Returns error if the strings are equal.
func AlphaMax(a, b string) (string, error) {
	if len(b) < len(a) {
		return a, nil
	}
	if len(a) < len(b) {
		return b, nil
	}

	for i := 0; i < len(a); i++ {
		if a[i] > b[i] {
			return a, nil
		}
		if a[i] < b[i] {
			return b, nil
		}
	}

	return "", errors.New(fmt.Sprintf("%q and %q seem to be equal", a, b))
}

// IncrementAlphaBranch takes a zettel ID that ends in an alphabetic character
// and returns its zettelkasten-ID increment. E.g: tmp.1a -> tmp.1b. Returns
// error on invalid input.
func IncrementAlphaBranch(id string) (string, error) {
	var alphabet = "abcdefghijklmnopqrstuvwxyz"

	if id[len(id)-1:] == "z" {
		return id + "a", nil // z -> za
	}

	for i, r := range alphabet {
		if string(r) == id[len(id)-1:] {
			return id[:len(id)-1] + string(alphabet[i+1]), nil
		}
	}

	return id, errors.New("invalid branch string")
}

func ResolveCommand() {
	id := shift(&os.Args)
	if id == "next" || id == "previous" {
		panic("not implemented yet")
	}

	filePath := path.Join(zetDir, id+".md")
	if !fileExists(filePath) {
		log.Fatalf("file does not exist: %q", filePath)
	}

	fmt.Println(filePath)
}

// TODO: open command
//	- `zet open ID` -> open file in edior

// 0.1 for testing with vim here

// TODO: code cleanup/refactoring
// TODO: resolve navigation
//	- `zet resolve next ID` -> next available file path in sequence of ID
//	- `zet resolve previous ID` -> previous available file path in sequence of ID, will go to parent if at top of branch
// TODO: zet grep command

// 0.2 here

// TODO: link command
//	- xclip on linux, pbcopy on darwin, ??? on windows
//	- w/ support for xxx.1a2b -> xxx.1a2b1.md
//	- `zet link path PATH` -> [[ID]]
//	- `zet link srcId destId` -> append dst with link to src on new line

// 0.3 here

// TODO: help/usage output
// TODO: tab completion
// TODO: goreleaser

// 1.0 here

// TODO: rename command
// TODO: extract command
//	- must update subtree IDs == rename command is needed

// 1.1 here

// further features past 1.0
// TODO: graft command
// TODO: prune command
// TODO: browse command - TUI
//	- look at gh for rendering markdown
//	- look at logbrowser for the tui stuff, dont overcomplicate
// TODO: more sophisticated search

func CreateCommand(prefix string) {

	entries, err := os.ReadDir(zetDir)
	if err != nil {
		log.Fatalf("Unable to read zettel dir '%s': %s", zetDir, err)
	}

	maxNum := 0
	dotSeparated := true
	numberPrefix := unicode.IsDigit(rune(prefix[len(prefix)-1]))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		suffix, found := strings.CutPrefix(e.Name(), prefix)
		if !found {
			continue
		}

		if suffix[0] == '.' {
			suffix = suffix[1:]
		} else {
			if !numberPrefix {
				dotSeparated = false
			}
		}

		if !unicode.IsDigit(rune(suffix[0])) {
			continue
		}

		id, _ := strings.CutSuffix(suffix, ".md")

		num, err := strconv.Atoi(id)
		if err != nil {
			log.Printf("unable to parse number: %s", err)
		}
		if num > maxNum {
			maxNum = num
		}
	}

	var zettelId string
	if dotSeparated {
		zettelId = fmt.Sprintf("%s.%d", prefix, maxNum+1)
	} else {
		zettelId = fmt.Sprintf("%s%d", prefix, maxNum+1)
	}

	filePath := createZettelFile(zettelId)

	openInEditor(filePath)
}

func openInEditor(path string) {
	var cmd *exec.Cmd
	if editor == "vim" || editor == "nvim" {
		cmd = exec.Command(editor, "+6", "-c", "startinsert", path)
	} else {
		cmd = exec.Command(editor, "+6", path)
	}

	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	err := cmd.Run()
	if err != nil {
		log.Fatalf("Unable to run editor (%s) command: %s", editor, err)
	}
}

func putOnClipBoard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {

	case "linux":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		return fmt.Errorf("Adding stuff to clipboard is not implemented on your platform")
	}

	buf := bytes.NewBuffer([]byte(text))
	cmd.Stdin = buf
	return cmd.Run()
}

func timestamp() string {
	format := "Mon 2006-01-02 15:04:05 MST"
	now := time.Now()
	ts := now.Format(format)
	return ts
}

func shift(stringSlice *[]string) string {
	ret := (*stringSlice)[0]
	*stringSlice = (*stringSlice)[1:]
	return ret
}
