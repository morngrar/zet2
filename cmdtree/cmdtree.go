// cmdtree is a small library to create command trees like what is possible
// with bonzai or cobra, but dead simple without external dependencies, and
// being vendorable.
//
// Copyright 2026 Svein-Kåre Bjørnsen. All files in this package are subject to
// the MIT license. See the included LICENSE file for terms.
package cmdtree

import (
	"fmt"
	"log"
	"os"
	"regexp"
)

// Cmd represents a command tree node. It may not have any subcommands, in
// which case it is a leaf node. However, if a command is run with arguments
// that do not match its sub-tree, the command's Exec function will be called
// with the arguments passed as parameters, so a Cmd that is an internal node
// in the overall tree can thus have separate functionality aside from just
// routing a command line to nodes below it.
type Cmd struct {

	// CommandName represents a command's name as used on the command line when
	// it is a subcommand. It is wise to set this value, even if the command in
	// question is your root node, since it can then be imported and used in
	// other command trees without extra work.
	CommandName string

	// Exec is the function that will run if you call this command with args
	// that do not match any of its SubCommands. Make sure to define this
	// function, as you will be dereferencing a null pointer otherwise.
	Exec func(args []string) error

	// SubCommands is a slice of all children of this command. The field
	// represents the sub-tree of this command node. All sub commands must have
	// their 'CommandName' set to a non-empty string, as this is used in tab
	// completion as well as execution of command lines. If you forget setting
	// this value, the command cannot be found by the Run function, and so will
	// not be executable.
	SubCommands []Cmd
}

// Complete will perform command completion based on 'complete -C' in bash or
// zsh (feature must be enabled in zsh). Run it on your COMP_LINE env variable,
// or better yet, just use the 'CompleteOrRun' function on the Cmd in question.
//
// NOTE: may panic in some error cases. Do not call this function in an event
// loop or similar, since it may crash your program. (Although I cannot fathom
// why you would want to do this..)
func (cmd Cmd) Complete(compslice []string) {
	if len(cmd.SubCommands) == 0 {
		fmt.Println("")
		return
	}

	alternatives := []string{}
	for _, c := range cmd.SubCommands {
		alternatives = append(alternatives, c.CommandName)
	}

	cmplen := len(compslice)
	switch cmplen {
	case 0:
		for _, c := range cmd.SubCommands {
			fmt.Println(c.CommandName)
		}
		return
	case 1:
		matches := []string{}
		word := compslice[0]
		for _, alt := range alternatives {
			match, err := regexp.MatchString(fmt.Sprintf("^%s", word), alt)
			if err != nil {
				panic(fmt.Errorf("regex failure while matching alternatives: %w", err))
			}
			if match {
				matches = append(matches, alt)
			}
		}
		if len(matches) == 0 {
			fmt.Println("")
			return
		} else if len(matches) == 1 && word == matches[0] {
			if found := cmd.recurseCompleteForSubcommand(compslice, word); found {
				return
			}
		}
		for _, m := range matches {
			fmt.Println(m)
		}

	default:
		word := compslice[0]
		for _, alt := range alternatives {
			if alt == word {
				if found := cmd.recurseCompleteForSubcommand(compslice, word); found {
					return
				}
			}
		}
		fmt.Println("")
	}
}

func (cmd Cmd) recurseCompleteForSubcommand(compslice []string, word string) bool {
	for _, c := range cmd.SubCommands {
		if c.CommandName == word {
			c.Complete(compslice[1:])
			return true
		}
	}
	return false
}

// CompleteOrRun is the function you should call in main for your root node. It
// will handle completion and running of the command for you. Although both Run
// and Complete are exported, you should normally not have to deal with these
// directly.
//
// NOTE: since this function calls Complete internally, it may also panic in
// case of some errors. Do not call this inside an event loop or similar. That
// would be a use case for using Run directly.
func (cmd *Cmd) CompleteOrRun() {
	line := os.Getenv("COMP_LINE")
	if line != "" {
		cmpslice := SpaceSplitAndClean(line)
		cmplen := len(cmpslice)
		if cmplen <= 0 {
			panic("compline slice length out of lower bound")
		}
		if cmplen == 1 {
			cmd.Complete(nil)
		} else if cmplen > 0 {
			cmd.Complete(cmpslice[1:])
		}
	} else {
		err := cmd.Run(os.Args)
		if err != nil {
			log.Fatalf("Error running cmd: %s", err)
		}
	}
}

// Run will interpret its argument list (normally passed os.Args) and run the
// corresponding subcommand, passint the optional remaining tail of the
// argument list as parameters. You should normally not have to deal with this
// function directly, but just call 'CompleteOrRun' in main. It is left
// exported for any edge cases that I have not yet thought of
func (cmd Cmd) Run(args []string) error {
	workingCmd := &cmd
	for {
		_, err := SliceShift(&args)
		if err != nil {
			return fmt.Errorf("cmd.Run unable to shift args: %w", err)
		}
		if len(args) == 0 {
			break
		}
		var nextCmd *Cmd = nil
		for i := 0; i < len(workingCmd.SubCommands); i++ {
			c := &workingCmd.SubCommands[i]
			if c.CommandName == args[0] {
				nextCmd = c
				break
			}
		}
		if nextCmd == nil {
			break
		}
		workingCmd = nextCmd
	}
	return workingCmd.Exec(args)
}
