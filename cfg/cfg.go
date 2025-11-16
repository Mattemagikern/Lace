package cfg

import (
	"os"
	"fmt"
	"strings"

	"github.com/Mattemagikern/flags"
)
var (
	TIME		string = ""
	VERSION 	string = ""
	Version 	= false
	Verbose 	= false
	Server 		= false
	Install 	= false
	Help   		= false
	Dryrun 		= false
	f 		= flags.NewFlagSet()
)

func Make() ([]string, error){
	f.SetFlag(&Version, flags.Key{"", "--version"}, "Prints version")
	f.SetFlag(&Verbose, flags.Key{"-v", "--verbose"}, "enables verbose logging")
	f.SetFlag(&Help, flags.Key{"-h", "--help"}, "Prints this message")
	f.SetFlag(&Dryrun, flags.Key{"", "--dryrun"}, "performs a dry run, no changes made")
	f.SetFlag(&Install, flags.Key{"", "--install"}, "installs this program on the remote server")
	f.SetFlag(&Server, flags.Key{"", "--server"}, "starts server instance")

	args, err := f.Parse(os.Args[1:])
	if err != nil {
		return nil, fmt.Errorf("failed to parse flags: %v", err)
	}
	return args, nil
}

func PrintHelpAndExit(){
	fmt.Printf("Usage: %s [options] [arguments]\n", os.Args[0])
	for _, line := range f.Help() {
		fmt.Println("\t" + strings.Join(line[:], ", "))
	}
	fmt.Println("Version: ", VERSION)
	os.Exit(0)
}
