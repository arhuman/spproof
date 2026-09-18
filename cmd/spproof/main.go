// Command spproof proves that a declared set of static checks holds over files.
//
// Exit codes: 0 every applicable rule held, 1 at least one violation, 2 the
// engine could not run. The 1/2 split is load-bearing: a policy syntax error
// must never read as clean code.
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
