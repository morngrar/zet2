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

	"golang.org/x/term"
)

var editor = os.Getenv("EDITOR")
var zetDir = "./zettel"
var defaultPrefix = "tmp"
var version = "v0.2.1"

var sequenceUpperLimit = 999999

// prefixes that are disallowed because they will come in conflict with
// subcommands
var reservedPrefixes = []string{
	"branch",
	"grep",
	"next",
	"previous",
	"version",
	"--version",
	"-v",
	"resolve",
	"open",
	"help",
	"path",
	"--help",
	"-h",
}

func main() {
	log.SetFlags(0) // turn off timestamping log statements, this is a cli app
	var err error

	// if os.Args[0] != "./zet2" { // if not running the command from same dir
	// 	homeDir, err := os.UserHomeDir()
	// 	if err != nil {
	// 		panic(err)
	// 	}
	// 	zetDir = path.Join(homeDir, "zettel2") // NOTE: temporary prod-dir until 1.0, then the trailing 2 will be dropped in command and dir
	// }

	err = os.MkdirAll(zetDir, os.ModePerm) // ensures existence of zettel dir
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
		for _, e := range []string{"version", "--version", "-v"} {
			if e == os.Args[0] {
				fmt.Printf("zet2 %s, Copyright 2025 S. Bjørnsen\n", version)
				return
			}
		}

		// check against reserved stuff
		for _, e := range reservedPrefixes {
			if e == os.Args[0] {
				panic("reserved") //TODO: do this better
			}
		}

		CreateCommand(os.Args[0])
		return
	}

	//NOTE: subcommand tree
	switch shift(&os.Args) {
	case "create":
		if len(os.Args) == 0 {
			panic("TODO: implement usage: need to pass prefix for creation")
		}

		CreateCommand(os.Args[0])

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
	case "open":
		if len(os.Args) == 0 {
			panic("TODO: implement usage: need to pass id to open")
		}
		OpenCommand()
	case "grep":
		if len(os.Args) == 0 {
			panic("TODO: implement usage: need to pass id to open")
		}
		GrepCommand()
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
	if strings.HasSuffix(parentId, ".md") {
		base := path.Base(parentId)
		parentId, _ = strings.CutSuffix(base, ".md")
	}

	fileName := fmt.Sprintf("%s.md", parentId)
	filePath := path.Join(zetDir, fileName)
	byteContent, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Unable to open parent '%s' for branching: %s", filePath, err)
	}

	content := string(byteContent)
	links := extractLinksFromContent(content)
	branches := filterBranches(links, parentId)
	next, err := nextBranch(branches)
	if err != nil {
		log.Fatalf("Unable to calculate next branch: %s", err)
	}
	branchId := parentId + next
	createZettelFile(branchId + "1") // start branches on sequence no. 1

	// output branch link
	fmt.Printf("[[%s]]\n", branchId)
}

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

func GrepCommand() {
	grepTerm := shift(&os.Args)
	re, err := regexp.Compile(grepTerm)
	if err != nil {
		log.Fatalf("Unable to compile regex term: %s", err)
	}
	terminalWidth, _, err := term.GetSize(0)
	if err != nil {
		panic(err)
	}
	entries, err := os.ReadDir(zetDir)
	if err != nil {
		log.Fatalf("Unable to read zettel dir '%s': %s", zetDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, found := strings.CutSuffix(e.Name(), ".md")
		if !found {
			continue
		}
		contentBytes, err := os.ReadFile(path.Join(zetDir, e.Name()))
		if err != nil {
			panic(err)
		}
		if re.Match(contentBytes) {
			content := string(contentBytes)
			for _, line := range strings.Split(content, "\n") {
				if re.MatchString(line) {
					prefix := fmt.Sprintf("%s: ", id)
					truncLimit := terminalWidth - len(prefix)
					line = strings.TrimSpace(line)
					if len(line) > truncLimit {
						line = line[:truncLimit-3] + "..."
					}
					fmt.Printf("%s%s\n", prefix, line)
				}
			}
		}
	}
}

func OpenCommand() {
	id := shift(&os.Args)
	if !unicode.IsDigit(rune(id[len(id)-1])) {
		// not a sequence no. so must be branch
		var err error
		id, err = getFirstSeqInBranch(id)
		if err != nil {
			// BUG: zet2 open tmp breaks here. Probably extract stuff below for
			// j1.1.x into function to also work with tmp.x and asdf.x
			//	- will probably also need some logic to confirm it is in fact top level
			panic(err)
		}
	}

	// NOTE: happy path, just open the file
	filePath := path.Join(zetDir, id+".md")
	if fileExists(filePath) {
		openInEditor(filePath)
	}

	// NOTE: attempt to be clever when user tries to open valid prefix
	allIds := getAllIds()
	idsMatchingPrefix := []string{}
	for _, e := range allIds {
		if strings.HasPrefix(e, id) {
			idsMatchingPrefix = append(idsMatchingPrefix, e)
		}
	}
	if len(idsMatchingPrefix) == 0 {
		log.Fatalf("File doesn't exist, and no matching prefixes found.")
	}

	// NOTE: find the start of the shortest matching id
	minNum := sequenceUpperLimit
	var base string
	for _, e := range idsMatchingPrefix {
		var seq string
		var err error
		base, seq, _, err = stripLeaf(e)
		if err != nil {
			log.Printf("Unable to strip sequence number: %s", err)
		}

		// NOTE: only sequences where the base is a match for the id are
		// interesting, because otherwise we'll match grandchildren
		if base != id+"." {
			continue
		}

		num, err := strconv.Atoi(seq)
		if err != nil {
			log.Printf("Unable to parse number: %s", err)
		}

		if num == sequenceUpperLimit {
			panic("unexpected high sequence number encountered. logic should be re-evaluated")
		}

		if num < minNum {
			minNum = num
		}
		if num == 0 {
			break
		}
	}

	if minNum < sequenceUpperLimit {
		newFile := fmt.Sprintf("%s%d.md", base, minNum)
		filePath = path.Join(zetDir, newFile)
		if fileExists(filePath) {
			openInEditor(filePath)
			return
		}
	}

	// NOTE: i tried.
	log.Fatalf("Neither file, nor matching sequence exist: %q", id)
}

func ResolveCommand() {
	id := shift(&os.Args)
	if id == "next" {
		pathOrId := shift(&os.Args)
		if pathOrId == "path" {
			zetPath := shift(&os.Args)
			base := path.Base(zetPath)
			id, extFound := strings.CutSuffix(base, ".md")
			if !extFound {
				log.Fatalf("given file did not have expected extension: %q", base)
			}
			_, nextPath, err := determineNextZet(id)
			if err != nil {
				log.Fatalf("Error determining next id: %s", err)
				//TODO: give usage info instead of just crashing
			}
			fmt.Println(nextPath)
			return
		} else {
			id := pathOrId
			nextId, _, err := determineNextZet(id)
			if err != nil {
				log.Fatalf("Error determining next id: %s", err)
				//TODO: give usage info instead of just crashing
			}
			fmt.Println(nextId)
			return
		}
	}

	if id == "previous" {
		pathOrId := shift(&os.Args)
		if pathOrId == "path" {
			zetPath := shift(&os.Args)
			base := path.Base(zetPath)
			id, extFound := strings.CutSuffix(base, ".md")
			if !extFound {
				log.Fatalf("given file did not have expected extension: %q", base)
			}
			_, prevPath, err := determinePrevZet(id)
			if err != nil {
				log.Fatalf("Error determining next id: %s", err)
				//TODO: give usage info instead of just crashing
			}
			fmt.Println(prevPath)
			return
		} else {
			id := pathOrId
			prevId, _, err := determinePrevZet(id)
			if err != nil {
				log.Fatalf("Error determining previous id: %s", err)
				//TODO: give usage info instead of just crashing
			}
			fmt.Println(prevId)
			return
		}
	}
	if !unicode.IsDigit(rune(id[len(id)-1])) {
		var err error
		id, err = getFirstSeqInBranch(id)
		if err != nil {
			panic(err) // TODO: dunno if this is the correct spot for the bug
		}

	}
	filePath := path.Join(zetDir, id+".md")
	if !fileExists(filePath) {
		log.Fatalf("file does not exist: %q", filePath)
	}
	fmt.Println(filePath)
}

// alphaMax takes two alphabetic strings and returns the one with the highest
// lexical value. Returns error if the strings are equal.
func alphaMax(a, b string) (string, error) {
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

func determineNextZet(id string) (nextId string, nextPath string, err error) {
	base, seq, _, err := stripLeaf(id)
	if err != nil {
		return nextId, nextPath, err
	}

	seqNum, err := strconv.Atoi(seq)
	if err != nil {
		return nextId, nextPath, err
	}

	nextId = fmt.Sprintf("%s%d", base, seqNum+1)
	nextPath = path.Join(zetDir, nextId+".md")

	if !fileExists(nextPath) {
		if strings.Contains(nextPath, "/") || strings.Contains(nextPath, "\\") {
			err = fmt.Errorf("next file %q doesn't exist. Did you mean to call the 'next path' subcommand?", nextPath)
		} else {
			err = fmt.Errorf("next file %q doesn't exist", nextPath)
		}
		return nextId, nextPath, err
	}
	return nextId, nextPath, nil
}

func determinePrevZet(id string) (prevId string, prevPath string, err error) {
	base, seq, _, err := stripLeaf(id)
	if err != nil {
		return prevId, prevPath, err
	}

	seqNum, err := strconv.Atoi(seq)
	if err != nil {
		return prevId, prevPath, err
	}

	prevNum := seqNum - 1
	if prevNum < 1 {
		prevId = fmt.Sprintf("%s%d", base, prevNum)
		prevPath = path.Join(zetDir, prevId+".md")
		if !fileExists(prevPath) {
			prevId = fmt.Sprintf("%s%d", base, 0)
			base, _, isDigit, err := stripLeaf(prevId)
			if err != nil {
				return prevId, prevPath, err
			}
			if !isDigit {
				return "", "", fmt.Errorf("branch seems to be numeric. Cannot resolve parent")
			}
			base, seq, isDigit, err = stripLeaf(base)
			if err != nil {
				return prevId, prevPath, err
			}
			if isDigit {
				return "", "", fmt.Errorf("branch seems to be numeric despite leaf being numeric, cannot resolve parent")
			}
			prevId = base
			prevPath = path.Join(zetDir, prevId+".md")
			if !fileExists(prevPath) {
				err = fmt.Errorf("previous file %q doesn't exist", prevPath)
				return prevId, prevPath, err
			}
		}
		return prevId, prevPath, nil
	}

	prevId = fmt.Sprintf("%s%d", base, prevNum)
	prevPath = path.Join(zetDir, prevId+".md")
	if !fileExists(prevPath) {
		//TODO: move this path-check earlier in tree to give helpful error messages in this case no matter the input id
		if strings.Contains(prevPath, "/") || strings.Contains(prevPath, "\\") {
			err = fmt.Errorf("previous file %q doesn't exist. Did you mean to call the 'previous path' subcommand?", prevPath)
		} else {
			err = fmt.Errorf("previous file %q doesn't exist", prevPath)
		}
		return prevId, prevPath, err
	}

	return prevId, prevPath, nil
}

// extractLinksFromContent takes the entire content of a zettel and extracts
// all links from it, stripping them of markup, leaving only the linked zettel
// IDs as a string slice.
func extractLinksFromContent(content string) []string {
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

func fileExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return err == nil || !os.IsNotExist(err)
}

// filterBranches takes a slice of links (as stripped zettel IDs) and a zettel
// ID, and filters out all links that are not direct branches of the zettel ID.
func filterBranches(links []string, parentId string) []string {
	// NOTE: Branches are always alphabetically suffixed. links to specific
	// zettels in a branch have the sequence number
	var branches []string
	for _, l := range links {
		base, _, digit, err := stripLeaf(l)
		if err != nil {
			log.Fatalf("error filtering branch: %s", err)
		}
		if digit {
			continue
		}
		if base == parentId {
			branches = append(branches, l)
		}
	}
	return branches
}

func getAllIds() []string {
	entries, err := os.ReadDir(zetDir)
	if err != nil {
		log.Fatalf("Unable to read zettel dir '%s': %s", zetDir, err)
	}
	ret := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, found := strings.CutSuffix(e.Name(), ".md")
		if !found {
			continue
		}
		ret = append(ret, id)
	}
	return ret
}

func getFirstSeqInBranch(id string) (string, error) {
	maxSeqVal := sequenceUpperLimit
	allIds := getAllIds()
	minSeq := maxSeqVal
	for _, e := range allIds {
		if strings.HasPrefix(e, id) {
			base, seq, _, err := stripLeaf(e)
			if err != nil {
				panic(err)
			}
			if base != id {
				continue
			}
			n, err := strconv.Atoi(seq)
			if err != nil {
				panic(err)
			}
			if n == 0 {
				minSeq = n
				break // cant go lower, stop search
			}
			if n < minSeq {
				minSeq = n
			}
		}
	}
	if minSeq == maxSeqVal {
		return "", fmt.Errorf("Unable to find branch: %q", id)
	}
	id = fmt.Sprintf("%s%d", id, minSeq)
	return id, nil
}

// incrementAlphaBranch takes a zettel ID that ends in an alphabetic character
// and returns its zettelkasten-ID increment. E.g: tmp.1a -> tmp.1b. Returns
// error on invalid input.
func incrementAlphaBranch(id string) (string, error) {
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

// nextBranch takes a list of sibling zettel IDs and returns the ID of the next
// upcoming branch on the parent zettel, as well as an error. In the case of an
// empty slice (no other children of current zettel), returns an 'a'.
func nextBranch(branches []string) (string, error) {

	// the first branch will always be 'a' in a numbered sceme
	if len(branches) == 0 {
		return "a", nil
	}

	var err error

	var isDigits bool
	maxChars := ""
	for _, branch := range branches {
		_, branch, isDigits, err = stripLeaf(branch)
		if err != nil {
			return "", fmt.Errorf("unable to strip branch leaf: %w", err)
		}

		// TODO: clean this up
		if isDigits {
			panic("Attempting to process branch that ends in a number. This is no longer applicable, and there must have happened a programming error. This is a bug.")
		} else {
			maxChars, err = alphaMax(maxChars, branch)
			if err != nil {
				return "", err
			}
		}
	}

	if maxChars == "" {
		panic("highest branch number not detected after iterating through all given branches in NextBranch")
	}

	nextAlphaBranch, err := incrementAlphaBranch(maxChars)
	if err != nil {
		return "", fmt.Errorf("unable to increment leaf: %w", err)
	}
	return nextAlphaBranch, nil
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

func shift(stringSlice *[]string) string {
	ret := (*stringSlice)[0]
	*stringSlice = (*stringSlice)[1:]
	return ret
}

// stripLeaf takes a zettel ID and strips the leaf branch off it, splitting the
// ID into its parent and child components.
//
// E.g: tmp.12.321aa32c69 -> 'tmp.12.321aa32c', '69'
//
// Returns base, branch, a boolean stating if the stripped leaf was numeric or
// not, and potentially an error.
func stripLeaf(id string) (string, string, bool, error) {
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

func timestamp() string {
	format := "Mon 2006-01-02 15:04:05 MST"
	now := time.Now()
	ts := now.Format(format)
	return ts
}

// 0.2.2 here

// TODO: link command
//	- xclip on linux, pbcopy on darwin, ??? on windows
//	- w/ support for xxx.1a2b -> xxx.1a2b1.md
//	- `zet link path PATH` -> [[ID]]
//	- `zet link srcId destId` -> append dst with link to src on new line
// TODO: branch subcommand that appends link to new branch in the parent, to be
// used from cli rather than in-editor
// TODO: resolve branch subcommand that returns branch prefix of given id or
// path: tmp.1asdf32 -> tmp.1asdf || .../tmp.1asdf32.md -> tmp.1asdf

// 0.3 here

// TODO: code cleanup/refactoring
// TODO: help/usage output
// TODO: tab completion
// TODO: automatic embed of latest git tag as version
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
