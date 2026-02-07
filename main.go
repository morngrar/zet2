package main

import (
	"bytes"
	"fmt"
	"io"
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

	"github.com/morngrar/zet2/cmdtree"
	"golang.org/x/term"
)

// NOTE: do `export ZET2_DEBUG=1` or `export ZET2_DEBUG=true` before running
// nvim to use local zettel folder instead of the system one
var DEBUG = os.Getenv("ZET2_DEBUG") == "1" || os.Getenv("ZET2_DEBUG") == "true"

var editor = os.Getenv("EDITOR")
var zetDir = "./zettel"
var defaultPrefix = "tmp"
var version = "v0.6.2"

var sequenceUpperLimit = 999999

// prefixes that are disallowed because they will come in conflict with
// subcommands
var reservedPrefixes = []string{
	"branch",
	"link",
	"grep",
	"next",
	"previous",
	"rename",
	"replant",
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

	if !DEBUG {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Unable to retrieve user's home directory: %s", err)
		}
		zetDir = path.Join(homeDir, "zettel2") // NOTE: temporary prod-dir until 1.0, then the trailing 2 will be dropped in command and dir
		// TODO: make this configurable
	}

	err = os.MkdirAll(zetDir, os.ModePerm) // ensures existence of zettel dir
	if err != nil {
		log.Fatalf("Unable to ensure zettel dir '%s': %s", zetDir, err)
	}

	ZetCommand.CompleteOrRun()
}

func printVersion(args []string) error {
	fmt.Printf("zet2 %s, Copyright 2026 S. Bjørnsen\n", version)
	return nil
}

var ZetCommand = cmdtree.Cmd{
	CommandName: "zet",
	SubCommands: []cmdtree.Cmd{
		CreateCommand,
		BranchCommand,
		GrepCommand,
		LinkCommand,
		OpenCommand,
		RenameCommand,
		// ReplantCommand,
		ResolveCommand,
		{
			CommandName: "version",
			Exec:        printVersion,
		},
	},
	Exec: func(args []string) error {
		if len(args) == 0 {
			return CreateCommand.Exec([]string{defaultPrefix})
		}

		// NOTE: special commands that should not be tab-completed
		switch args[0] {
		case "--version":
			return printVersion(args)
		}

		return CreateCommand.Exec(args)
	},
}

var BranchCommand = cmdtree.Cmd{
	CommandName: "branch",
	SubCommands: []cmdtree.Cmd{
		{
			CommandName: "link",
			Exec: func(args []string) error {
				parentId, err := cmdtree.SliceShift(&args)
				if err != nil {
					return fmt.Errorf("unable to shift off parent id for branch link command: %w", err)
				}

				branchId, err := createZettelBranchFile(parentId)
				if err != nil {
					return fmt.Errorf("error while creating branch file: %w", err)
				}

				linkAndAppend(parentId, branchId)
				beginning, err := getFirstSeqInBranch(branchId)
				if err != nil {
					panic(err) // should NEVER happen
				}
				newFile := fmt.Sprintf("%s.md", beginning)
				filePath := path.Join(zetDir, newFile)
				fmt.Printf("%s\n", filePath)
				return nil
			},
		},
	},
	Exec: func(args []string) error {
		// NOTE: how to make sure that the file names in the system and the links
		// are always in sync?
		//	- normally, zets are write-only, except for renaming and extraction and
		//	possible corruption/deletion
		//	- if the zettel is master, the functions for modification must account
		//	for updating no-longer-valid links
		//	- if the file system is master, the situation is more unknown, and
		//	responsibilities aren't clear
		//	- therefore the zettel should be master

		parentId, err := cmdtree.SliceShift(&args)
		if err != nil {
			return fmt.Errorf("Error getting first argument of branch command: %w", err)
		}
		branchId, err := createZettelBranchFile(parentId)
		if err != nil {
			return fmt.Errorf("error while creating branch file: %w", err)
		}

		fmt.Printf("[[%s]]\n", branchId)
		return nil
	},
}

func createZettelBranchFile(parentId string) (branchId string, err error) {
	if strings.HasSuffix(parentId, ".md") {
		base := path.Base(parentId)
		parentId, _ = strings.CutSuffix(base, ".md")
	}

	fileName := fmt.Sprintf("%s.md", parentId)
	filePath := path.Join(zetDir, fileName)
	byteContent, err := os.ReadFile(filePath)
	if err != nil {
		return branchId, fmt.Errorf("unable to open parent '%s' for branching: %w", filePath, err)
	}

	content := string(byteContent)
	links := extractLinksFromContent(content)
	branches, err := filterBranches(links, parentId)
	if err != nil {
		return branchId, fmt.Errorf("unable to filter branches: %w", err)
	}
	next, err := nextBranch(branches)
	if err != nil {
		return branchId, fmt.Errorf("unable to calculate next branch: %w", err)
	}
	branchId = parentId + next
	_, err = createZettelFile(branchId + "1") // start branches on sequence no. 1
	if err != nil {
		return branchId, fmt.Errorf("error while creating zettel file for branch %q: %w", branchId, err)
	}
	return branchId, nil
}

// finds the lowest number in a sequence or branch, and returns it along with
// if the sequence prefix is a dotted one or not. Returns error if the list of
// entries is empty, or if the prefix is not found.
func findLowestNumInSeq(prefix string, entries []os.DirEntry) (int, bool, error) {

	if len(entries) == 0 {
		return 0, false, fmt.Errorf("entries was empty")
	}

	minNum := sequenceUpperLimit
	dotSeparated := true
	numberPrefix := unicode.IsDigit(rune(prefix[len(prefix)-1]))
	prefixFound := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		suffix, found := strings.CutPrefix(e.Name(), prefix)
		if !found {
			continue
		}
		prefixFound = true
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
			continue
		}
		if num < minNum {
			minNum = num
		}
	}

	if !prefixFound {
		return minNum, dotSeparated, fmt.Errorf("prefix %q not found", prefix)
	}

	return minNum, dotSeparated, nil
}

// finds the highest number in a sequence or branch, and returns it along with
// if the sequence prefix is a dotted one or not. Returns error if the list of
// entries is empty, or if the prefix is not found.
func findHighestNumInSeq(prefix string, entries []os.DirEntry) (int, bool, error) {

	if len(entries) == 0 {
		return 0, false, fmt.Errorf("entries was empty")
	}

	maxNum := 0
	dotSeparated := true
	numberPrefix := unicode.IsDigit(rune(prefix[len(prefix)-1]))
	prefixFound := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		suffix, found := strings.CutPrefix(e.Name(), prefix)
		if !found {
			continue
		}
		prefixFound = true
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
			continue
		}
		if num > maxNum {
			maxNum = num
		}
	}

	if !prefixFound {
		return maxNum, dotSeparated, fmt.Errorf("prefix %q not found", prefix)
	}

	return maxNum, dotSeparated, nil
}

var CreateCommand = cmdtree.Cmd{
	CommandName: "create",
	Exec: func(args []string) error {
		entries, err := os.ReadDir(zetDir)
		if err != nil {
			return fmt.Errorf("unable to read zettel dir %q: %w", zetDir, err)
		}

		prefix, err := cmdtree.SliceShift(&args)
		if err != nil {
			return fmt.Errorf("expected prefix to be an argument, error encountered while shifting it: %w", err)
		}

		// NOTE: check against reserved stuff
		for _, e := range reservedPrefixes {
			if e == prefix {
				return fmt.Errorf("reserved prefix")
			}
		}

		maxNum, dotSeparated, err := findHighestNumInSeq(prefix, entries)
		if err != nil {
			// NOTE: first zettel with given prefix
			dotSeparated = true
			maxNum = 0
		}
		var zettelId string
		if dotSeparated {
			zettelId = fmt.Sprintf("%s.%d", prefix, maxNum+1)
		} else {
			zettelId = fmt.Sprintf("%s%d", prefix, maxNum+1)
		}
		filePath, err := createZettelFile(zettelId)
		if err != nil {
			return fmt.Errorf("error while creating file while creating new zettel: %w", err)
		}
		return openInEditor(filePath, true)
	},
}

var GrepCommand = cmdtree.Cmd{
	CommandName: "grep",
	Exec: func(args []string) error {
		grepTerm, err := cmdtree.SliceShift(&args)
		if err != nil {
			return fmt.Errorf("error while shifting off grep term from args: %w", err)
		}
		re, err := regexp.Compile(grepTerm)
		if err != nil {
			return fmt.Errorf("unable to compile regex term: %w", err)
		}
		terminalWidth, _, err := term.GetSize(0)
		if err != nil {
			return fmt.Errorf("error while getting size of terminal: %w", err)
		}
		entries, err := os.ReadDir(zetDir)
		if err != nil {
			return fmt.Errorf("unable to read zettel dir %q: %w", zetDir, err)
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
				return fmt.Errorf("error while reading file: %w", err)
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
		return nil
	},
}

func retryOpenPrefix(id string) error {
	idsMatchingPrefix, err := getIdsMatchingPrefix(id)
	if err != nil {
		return fmt.Errorf("failed getting ids matching prefix %q: %w", id, err)
	}
	if len(idsMatchingPrefix) == 0 {
		return fmt.Errorf("file doesn't exist, and no matching prefixes found.")
	}

	// NOTE: find the start of the shortest matching id
	// TODO: the for loop below looks a lot like `getFirstSeqInBranch`, refactoring opportunity
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
			continue
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

	if base != id && base != id+"." {
		base = id + "."
	}

	if minNum < sequenceUpperLimit {
		newFile := fmt.Sprintf("%s%d.md", base, minNum)
		filePath := path.Join(zetDir, newFile)
		if fileExists(filePath) {
			return openInEditor(filePath, false)
		}
	}

	// NOTE: i tried.
	return fmt.Errorf("neither file, nor matching sequence exist: %q", id)
}

func filterPassthrough() error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to read stdin data: %w", err)
	}
	_, err = fmt.Fprint(os.Stdout, string(data))
	if err != nil {
		return fmt.Errorf("failed to write data to stdout: %w", err)
	}
	return nil
}

var LinkCommand = cmdtree.Cmd{
	CommandName: "link",
	SubCommands: []cmdtree.Cmd{
		{
			CommandName: "path",
			Exec: func(args []string) error {
				if len(args) != 1 {
					return fmt.Errorf("unsupported number of trailing arguments to link path command: '%v'", args)
				}
				id, err := getIdFromPathOnArgs(&args)
				if err != nil {
					return fmt.Errorf("failed to get id from args: %w", err)
				}
				s := fmt.Sprintf("[[%s]]\n", id)
				err = putOnClipboard(s)
				if err != nil {
					return fmt.Errorf("error while adding link to clipboard: %w", err)
				}

				// NOTE: if called as filter with something on stdin, e.g. ran
				// by a keybind in vim, should write that content back on
				// stdout
				err = filterPassthrough()
				if err != nil {
					return fmt.Errorf("failed to pass through filtered data: %w", err)
				}

				return nil
			},
		},
	},

	Exec: func(args []string) error {
		srcId, err := cmdtree.SliceShift(&args)
		if err != nil {
			return fmt.Errorf("unable to shift off source id from args: %w", err)
		}

		if len(args) > 1 {
			return fmt.Errorf("unsupported number of trailing arguments to link command: '%v'", args)
		}

		// TODO: two-way linking between ids?

		dstId, err := cmdtree.SliceShift(&args)
		if err != nil {
			return fmt.Errorf("failed to shift off destination ID in link command: %w", err)
		}
		return linkAndAppend(srcId, dstId)
	},
}

func linkAndAppend(srcId, dstId string) error {
	srcPath := path.Join(zetDir, srcId+".md")
	if !fileExists(srcPath) {
		return fmt.Errorf("source zet does not exist: %q", srcPath)
	}

	dstPath := path.Join(zetDir, dstId+".md")
	if !fileExists(dstPath) {
		_, err := getFirstSeqInBranch(dstId)
		if err != nil {
			return fmt.Errorf("destination %q does not exist: %w", dstId, err)
		}
	}

	link := fmt.Sprintf("\n[[%s]]\n", dstId)
	err := appendToFile(link, srcPath)
	if err != nil {
		return fmt.Errorf("unable to append link: %w", err)
	}
	return nil
}

func appendToFile(s string, path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		return fmt.Errorf("failed to append string to %q: %w", path, err)
	}
	return nil
}

var OpenCommand = cmdtree.Cmd{
	CommandName: "open",
	Exec: func(args []string) error {
		var err error
		id, err := cmdtree.SliceShift(&args)
		if err != nil {
			return fmt.Errorf("error while shifting off id to open: %w", err)
		}
		if !unicode.IsDigit(rune(id[len(id)-1])) {
			// NOTE: not a sequence no. so must be branch
			tmpId, err := getFirstSeqInBranch(id)
			if err != nil {
				// NOTE: may be an all-letter prefix
				return retryOpenPrefix(id)
			}
			id = tmpId // NOTE: success should preserve new id
		}

		// NOTE: happy path, just open the file
		filePath := path.Join(zetDir, id+".md")
		if fileExists(filePath) {
			return openInEditor(filePath, false)
		}

		// NOTE: attempt to be clever when user tries to open valid prefix
		return retryOpenPrefix(id)
	},
}

var RenameCommand = cmdtree.Cmd{
	CommandName: "rename",
	Exec: func(args []string) error {
		from, err := cmdtree.SliceShift(&args)
		if err != nil {
			return fmt.Errorf("error while shifting off from id: %w", err)
		}
		to, err := cmdtree.SliceShift(&args)
		if err != nil {
			return fmt.Errorf("error while shifting off to id: %w", err)
		}
		renamedChildren := []string{}
		err = renameZettel(zetDir, from, to, &renamedChildren)
		if err != nil {
			return fmt.Errorf("failed to perform recursive rename: %w", err)
		}

		// for later
		// TODO: journaler
		// TODO: journal remover

		return nil
	},
}

var ReplantCommand = cmdtree.Cmd{
	CommandName: "replant",
	Exec: func(args []string) error {
		// NOTE: for renaming a branch into a new series, replacing branch link

		// NOTE: branch isolation from excalidraw:
		// 1. Determine parent ID
		// 2. Remove link(s) in parent (should replace with a jumpable prefix link instead)
		// 3. Get all zets prefixed with branch ID
		// 4. Rename all those according to [Renaming a Zettel] with new prefix,
		//    preserving sequence number

		// for branch isolation
		// TODO: link replacer
		return fmt.Errorf("replanting not implemented")
	},
}

// The OS function for renaming files will silently overwrite the destination
// file, if it exists. This is not acceptable in the present case, so we wrap
// it and check for existence first. Returning an error in case of an existing
// file or directory in the destination path.
func performRename(src, dst string) error {
	if fileExists(dst) {
		return fmt.Errorf("destination file %q exists", dst)
	}
	return os.Rename(src, dst)
}

func resolveSentinelZet(prefix string, start bool) (string, error) {
	entries, err := os.ReadDir(zetDir)
	if err != nil {
		return "", fmt.Errorf("Unable to read zettel dir %q: %w", zetDir, err)
	}
	var num int
	var dotSeparated bool
	if start {
		num, dotSeparated, err = findLowestNumInSeq(prefix, entries)
		if err != nil {
			return "", fmt.Errorf("Unable to find earliest number in sequence: %w", err)
		}
	} else {
		num, dotSeparated, err = findHighestNumInSeq(prefix, entries)
		if err != nil {
			return "", fmt.Errorf("Unable to find latest number in sequence: %w", err)
		}
	}
	var zettelId string
	if dotSeparated {
		zettelId = fmt.Sprintf("%s.%d", prefix, num)
	} else {
		zettelId = fmt.Sprintf("%s%d", prefix, num)
	}
	return zettelId, nil
}

var ResolveCommand = cmdtree.Cmd{
	CommandName: "resolve",
	SubCommands: []cmdtree.Cmd{
		{
			CommandName: "next",
			SubCommands: []cmdtree.Cmd{
				{
					CommandName: "path",
					Exec: func(args []string) error {
						zetPath, err := cmdtree.SliceShift(&args)
						if err != nil {
							return fmt.Errorf("failed to shift off zettel path: %w", err)
						}
						base := path.Base(zetPath)
						id, extFound := strings.CutSuffix(base, ".md")
						if !extFound {
							return fmt.Errorf("given file did not have expected extension: %q", base)
						}
						_, nextPath, err := determineNextZet(id)
						if err != nil {
							return fmt.Errorf("error determining next id: %s", err)
						}
						fmt.Println(nextPath)
						return nil
					},
				},
			},
			Exec: func(args []string) error {
				id, err := cmdtree.SliceShift(&args)
				if err != nil {
					return fmt.Errorf("failed to shift off id to resolve: %w", err)
				}
				nextId, _, err := determineNextZet(id)
				if err != nil {
					return fmt.Errorf("failed to determine next zettel: %w", err)

				}
				fmt.Println(nextId)
				return nil
			},
		},
		{
			CommandName: "latest",
			Exec: func(args []string) error {
				prefix, err := cmdtree.SliceShift(&args)
				if err != nil {
					return fmt.Errorf("failed shifting prefix off args: %w", err)
				}
				resolved, err := resolveSentinelZet(prefix, false)
				if err != nil {
					return fmt.Errorf("error while resolving sentinel zet for resolve latest command: %w", err)
				}
				fmt.Println(resolved)
				return nil
			},
		},
		{
			CommandName: "earliest",
			Exec: func(args []string) error {
				prefix, err := cmdtree.SliceShift(&args)
				if err != nil {
					return fmt.Errorf("failed shifting prefix off args: %w", err)
				}
				resolved, err := resolveSentinelZet(prefix, true)
				if err != nil {
					return fmt.Errorf("error while resolving sentinel zet in 'earliest' resolve command: %w", err)
				}
				fmt.Println(resolved)
				return nil
			},
		},
		{
			CommandName: "previous",
			SubCommands: []cmdtree.Cmd{
				{
					CommandName: "path",
					Exec: func(args []string) error {
						idOrSubcommand, err := getIdFromPathOnArgs(&args)
						if err != nil {
							return fmt.Errorf("error while getting id from args in resolve previous path command: %w", err)
						}
						_, prevPath, err := determinePrevZet(idOrSubcommand)
						if err != nil {
							return fmt.Errorf("error determining previous zettel path: %w", err)
						}
						fmt.Println(prevPath)
						return nil
					},
				},
			},
			Exec: func(args []string) error {
				pathOrId, err := cmdtree.SliceShift(&args)
				if err != nil {
					return fmt.Errorf("failed to shift args in resolve previous command: %w", err)
				}
				id := pathOrId
				prevId, _, err := determinePrevZet(id)
				if err != nil {
					return fmt.Errorf("error determining previous id: %w", err)
				}
				fmt.Println(prevId)
				return nil
			},
		},
	},
	Exec: func(args []string) error {
		id, err := cmdtree.SliceShift(&args)
		if err != nil {
			return fmt.Errorf("failed to shift id off args in resolve command: %w", err)
		}
		resolved := id
		if !unicode.IsDigit(rune(id[len(id)-1])) {
			resolved, err = resolveSentinelZet(id, true)
			if err != nil {
				return fmt.Errorf("error while resolving sentinel zet for normal resolve command")
			}
		}

		filePath := path.Join(zetDir, resolved+".md")
		if !fileExists(filePath) {
			resolved, err = resolveSentinelZet(id, true)
			if err != nil {
				return fmt.Errorf("error while resolving sentinel zet after determinin file didn't exist: %w", err)
			}
			filePath = path.Join(zetDir, resolved+".md")
			if !fileExists(filePath) {
				return fmt.Errorf("file does not exist: %q", filePath)
			}
		}
		fmt.Println(filePath)
		return nil
	},
}

// getIdFromPathOnArgs shifts os.Args and returns the zettel id of the file
// path that is assumed to be the first argument
func getIdFromPathOnArgs(args *[]string) (string, error) {
	zetPath, err := cmdtree.SliceShift(args)
	if err != nil {
		return "", fmt.Errorf("failed to shift off arg: %w", err)
	}
	base := path.Base(zetPath)
	id, extFound := strings.CutSuffix(base, ".md")
	if !extFound {
		return "", fmt.Errorf("given file did not have expected extension: %q", base)
	}
	return id, nil
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
	return "", fmt.Errorf("%q and %q seem to be equal", a, b)
}

func createZettelFile(zettelId string) (string, error) {
	fileName := fmt.Sprintf("%s.md", zettelId)
	filePath := path.Join(zetDir, fileName)
	if fileExists(filePath) {
		return "", fmt.Errorf("attempted to create existing file: %s", filePath)
	}
	f, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("unable to create file %q: %w", filePath, err)
	}
	defer f.Close()

	ts := timestamp()
	content := fmt.Sprintf("---\nzettel: %s\ndate: %s\n---\n\n\n\n", zettelId, ts)
	_, err = f.Write([]byte(content))
	if err != nil {
		return "", fmt.Errorf("failed to write content to new zettel file: %w", err)
	}
	return filePath, nil
}

func getIdsMatchingPrefix(prefix string) ([]string, error) {
	allIds, err := getAllIds()
	if err != nil {
		return nil, fmt.Errorf("failed retrieving all ids: %w", err)
	}
	idsMatchingPrefix := []string{}
	for _, e := range allIds {
		if strings.HasPrefix(e, prefix) {
			idsMatchingPrefix = append(idsMatchingPrefix, e)
		}
	}
	return idsMatchingPrefix, nil
}

func skipResolve(base string, seqNum int, reverse bool) (nextId, nextPath string, err error) {
	allMatchingIds, err := getIdsMatchingPrefix(base)
	if err != nil {
		err = fmt.Errorf("failed to get ids matching prefix %q: %w", base, err)
		return
	}
	reverseMaxMatch := -1
	for _, id := range allMatchingIds {
		subBase, subSeq, _, err := stripLeaf(id)
		if err != nil {
			panic(err) // TODO: figure out what to do here
		}
		if subBase == base {
			subSeqNum, err := strconv.Atoi(subSeq)
			if err != nil {
				return nextId, nextPath, err
			}
			if !reverse {
				if subSeqNum > seqNum {
					nextId = fmt.Sprintf("%s%d", base, subSeqNum)
					nextPath = path.Join(zetDir, nextId+".md")
					return nextId, nextPath, err
				}
			} else {
				if (subSeqNum < seqNum) && (reverseMaxMatch < subSeqNum) {
					reverseMaxMatch = subSeqNum
				}
			}
		}

	}
	if reverse {
		if reverseMaxMatch != -1 {
			nextId = fmt.Sprintf("%s%d", base, reverseMaxMatch)
			nextPath = path.Join(zetDir, nextId+".md")
			return nextId, nextPath, err
		}
	}

	return nextId, nextPath, fmt.Errorf("no available zettel to skip to")
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
		nextId, nextPath, err = skipResolve(base, seqNum, false)
		if err != nil {
			if strings.Contains(nextPath, "/") || strings.Contains(nextPath, "\\") {
				err = fmt.Errorf("next file %q doesn't exist. Did you mean to call the 'next path' subcommand?", nextPath)
			} else {
				err = fmt.Errorf("next file %q doesn't exist", nextPath)
			}
			return nextId, nextPath, err
		}
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
		prevId, prevPath, err = skipResolve(base, seqNum, true)
		if err != nil {
			//TODO: move this path-check earlier in tree to give helpful error messages in this case no matter the input id
			if strings.Contains(prevPath, "/") || strings.Contains(prevPath, "\\") {
				err = fmt.Errorf("previous file %q doesn't exist. Did you mean to call the 'previous path' subcommand?", prevPath)
			} else {
				err = fmt.Errorf("previous file %q doesn't exist", prevPath)
			}
			return prevId, prevPath, err
		}
	}

	return prevId, prevPath, nil
}

// extractLinksFromContent takes the entire content of a zettel and extracts
// all links from it, stripping them of markup, leaving only the linked zettel
// IDs as a string slice.
func extractLinksFromContent(content string) []string {
	var links []string
	r := regexp.MustCompile(`\[\[(?P<link>[a-zA-Z0-9\.\-\_]+)\]\]`)
	for line := range strings.SplitSeq(content, "\n") {
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
func filterBranches(links []string, parentId string) ([]string, error) {
	// NOTE: Branches are always alphabetically suffixed. links to specific
	// zettels in a branch have the sequence number
	var branches []string
	for _, l := range links {
		base, _, digit, err := stripLeaf(l)
		if err != nil {
			return nil, fmt.Errorf("error stripping leaf while filtering branches: %w", err)
		}
		if digit {
			continue
		}
		if base == parentId {
			branches = append(branches, l)
		}
	}
	return branches, nil
}

func getAllIds() ([]string, error) {
	entries, err := os.ReadDir(zetDir)
	if err != nil {
		return nil, fmt.Errorf("unable to read zettel dir %q: %w", zetDir, err)
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
	return ret, nil
}

func getFirstSeqInBranch(id string) (string, error) {
	maxSeqVal := sequenceUpperLimit
	allIds, err := getAllIds()
	if err != nil {
		return "", fmt.Errorf("failed retrieving all ids: %w", err)
	}
	minSeq := maxSeqVal
	for _, e := range allIds {
		if strings.HasPrefix(e, id) {
			base, seq, _, err := stripLeaf(e)
			if err != nil {
				return "", fmt.Errorf("failed to strip leaf of zettel ID: %w", err)
			}
			if base != id {
				continue
			}
			n, err := strconv.Atoi(seq)
			if err != nil {
				return "", fmt.Errorf("failed converting sequence to number: %w", err)
			}

			if n == maxSeqVal {
				panic("encountered unexpectedly large sequence, logic should be revisited")
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

	return id, fmt.Errorf("invalid branch string")
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
				return "", fmt.Errorf("failed to calculate alphaMax while computing next branch: %w", err)
			}
		}
	}

	if maxChars == "" {
		panic("highest branch number not detected after iterating through all given branches in nextBranch")
	}

	nextAlphaBranch, err := incrementAlphaBranch(maxChars)
	if err != nil {
		return "", fmt.Errorf("unable to increment leaf: %w", err)
	}
	return nextAlphaBranch, nil
}

func openInEditor(path string, insertMode bool) error {
	var cmd *exec.Cmd
	if (editor == "vim" || editor == "nvim") && insertMode {
		cmd = exec.Command(editor, "+6", "-c", "startinsert", path)
	} else {
		cmd = exec.Command(editor, "+6", path)
	}

	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	return cmd.Run()
}

func putOnClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {

	case "linux":
		if os.Getenv("XDG_SESSION_TYPE") == "wayland" {
			// ref: https://superuser.com/questions/1189467/how-to-copy-text-to-the-clipboard-when-using-wayland
			cmd = exec.Command("wl-copy")
		} else {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		}
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		return fmt.Errorf("Adding stuff to clipboard is not implemented on your platform")
	}

	buf := bytes.NewBuffer([]byte(text))
	cmd.Stdin = buf
	return cmd.Run()
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

func renameZettel(zettelDir, fromId, toId string, renamedChildren *[]string) error {

	// TODO: rewrite this function to not be recursive instead. split up
	// renaming and re-linking. This interface is UGLY. This function is
	// inefficient as f.

	if renamedChildren == nil {
		panic("must pass renamed children slice - i know this is tech debt")
	}

	var err error

	fmt.Printf("Renaming %q -> %q\n", fromId, toId)

	_, _, numeric, err := stripLeaf(toId)
	if err != nil {
		return fmt.Errorf("unable to strip leaf: %w", err)
	}

	if !numeric {
		return fmt.Errorf("toId refers to a branch, not a zettel")
	}

	// NOTE: validation done, rename file(s)
	fromPath := path.Join(zettelDir, fromId+".md")
	toPath := path.Join(zettelDir, toId+".md")

	err = performRename(fromPath, toPath)
	if err != nil {
		return fmt.Errorf("failed to rename %q: %w", fromPath, err)
	}

	buf, err := os.ReadFile(toPath)
	if err != nil {
		return fmt.Errorf("failed to read file %q for updating yaml: %w", toPath, err)
	}
	updatedContent := updateYamlPreamble(string(buf), toId)
	err = os.WriteFile(toPath, []byte(updatedContent), 0644)
	if err != nil {
		return fmt.Errorf("failed to write updated yaml: %w", err)
	}

	allIds, err := getAllIds()
	if err != nil {
		return fmt.Errorf("failed retrieving all ids: %w", err)
	}
	oldLink := fmt.Sprintf("[[%s]]", fromId)
	newLink := fmt.Sprintf("[[%s]]", toId)
	for _, id := range allIds {
		fname := path.Join(zettelDir, id+".md")
		buf, err := os.ReadFile(fname)
		if err != nil {
			return fmt.Errorf("failed to read file %q to update links: %w", fname, err)
		}
		newContent := strings.ReplaceAll(string(buf), oldLink, newLink)
		if newContent != string(buf) {
			err = os.WriteFile(fname, []byte(newContent), 0644)
			if err != nil {
				return fmt.Errorf("failed to write file %q after updating links: %w", fname, err)
			}
		}
	}

	prefix := fromId

collectRenamedChildrenLoop:
	for _, id := range allIds {

		for _, e := range *renamedChildren {
			if e == id {
				continue collectRenamedChildrenLoop
			}
		}

		idRunes := []rune(id)
		idLastRune := idRunes[len(idRunes)-1]
		idIsDigit := unicode.IsDigit(idLastRune)

		if err != nil {
			log.Fatalf("failed to strip leaf of id while renaming: %s", err)
		}
		if tail, ok := strings.CutPrefix(id, prefix); ok {

			// NOTE: if last digit of prefix and first digit of tail are both
			// of same class, then this is not a branch boundary. E.g. tmp.1 vs
			// tmp.123
			tailRunes := []rune(tail)
			tailIsDigit := unicode.IsDigit(tailRunes[0])
			if idIsDigit == tailIsDigit {
				continue
			}

			err = renameZettel(zettelDir, id, toId+tail, renamedChildren)
			if err != nil {
				log.Println("successfully renamed the following before error:")
				for _, c := range *renamedChildren {
					log.Printf("  %s\n", c)
				}
				return fmt.Errorf("failed to rename %q:", err)
			}
			*renamedChildren = append(*renamedChildren, id)
		}
	}

	originalContent := updatedContent
	linkedIds := extractLinksFromContent(updatedContent)
	for _, id := range linkedIds {
		base, branch, isDigit, err := stripLeaf(id)
		if err != nil {
			return fmt.Errorf("unable to strip leaf of %q for link renaming: %w", fromId, err)
		}

		if isDigit {
			continue
		}

		if base == fromId {
			oldLink := fmt.Sprintf("[[%s]]", id)
			newLink := fmt.Sprintf("[[%s%s]]", toId, branch)
			updatedContent = strings.ReplaceAll(updatedContent, oldLink, newLink)
		}
	}
	if updatedContent != originalContent {
		err = os.WriteFile(toPath, []byte(updatedContent), 0644)
		if err != nil {
			return fmt.Errorf("failed to write updated branch links to %q: %w", toPath, err)
		}
	}

	allIds, err = getAllIds()
	if err != nil {
		return fmt.Errorf("failed retrieving all ids: %w", err)
	}
	for _, root := range allIds {
		thePath := path.Join(zettelDir, root+".md")
		buf, err := os.ReadFile(thePath)
		if err != nil {
			return fmt.Errorf("failed to read file %q for updating branch links: %w", toPath, err)
		}
		content := string(buf)

		linkedIds := extractLinksFromContent(content)
		for _, id := range linkedIds {
			base, branch, isDigit, err := stripLeaf(id)
			if err != nil {
				return fmt.Errorf("unable to strip leaf of %q for link renaming: %w", fromId, err)
			}

			if isDigit {
				continue
			}

			if base == fromId {
				oldLink := fmt.Sprintf("[[%s]]", id)
				newLink := fmt.Sprintf("[[%s%s]]", toId, branch)
				content = strings.ReplaceAll(content, oldLink, newLink)
			}
		}

		if string(buf) != content {
			err = os.WriteFile(thePath, []byte(content), 0644)
			if err != nil {
				return fmt.Errorf("failed to write updated branch links to %q: %w", toPath, err)
			}
		}
	}

	fmt.Printf("Finished renaming %q -> %q\n", fromId, toId)

	return nil
}

func updateYamlPreamble(content, newId string) string {

	lines := strings.Split(content, "\n")
	preableStarted := false
	preableEnded := false
	noPreamble := true
	zettelKeyFound := false

	tmpPreamble := []string{}
	tmpContent := []string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !preableStarted && trimmed != "---" && trimmed != "" {
			break
		}

		if preableStarted && trimmed == "---" {
			preableEnded = true
			if !zettelKeyFound {
				tmpPreamble = append(tmpPreamble, fmt.Sprintf("zettel: %s", newId))
			}
			continue
		}

		if !preableStarted && trimmed == "---" {
			preableStarted = true
			noPreamble = false
			continue
		}

		if preableStarted && !preableEnded {
			if strings.HasPrefix(trimmed, "zettel:") {
				newLine := fmt.Sprintf("zettel: %s", newId)
				tmpPreamble = append(tmpPreamble, newLine)
				zettelKeyFound = true
			} else {
				tmpPreamble = append(tmpPreamble, trimmed)
			}
		}

		if preableEnded {
			tmpContent = append(tmpContent, line)
		}
	}

	if noPreamble {
		newContent := fmt.Sprintf("---\nzettel: %s\n---\n\n%s", newId, content)
		return newContent
	}

	newPreamble := strings.Join(tmpPreamble, "\n")
	newContent := strings.Join(tmpContent, "\n")
	newContent = fmt.Sprintf("---\n%s\n---\n%s", newPreamble, newContent)

	return newContent
}

// 0.6 here

// TODO: replant command

// 0.7 here

// TODO: extract command
//	- must update subtree IDs == rename command is needed
//	- furthermore, must effectively graft any child branches in selection onto new note

// 0.8 here

// TODO: add persistent configuration of default prefix

// 0.9 here

// TODO: code cleanup/refactoring
// TODO: help/usage output

// 0.10 here

// TODO: automatic embed of latest git tag as version
// TODO: goreleaser

// 1.0 here

// further features past 1.0
// TODO: resolve branch subcommand that returns branch prefix of given id or
// path: tmp.1asdf32 -> tmp.1asdf || .../tmp.1asdf32.md -> tmp.1asdf
// TODO: graft command
// TODO: prune command
// TODO: browse command - TUI
//	- look at gh for rendering markdown
//	- look at logbrowser for the tui stuff, dont overcomplicate
// TODO: more sophisticated search
// TODO: windows support for link command
