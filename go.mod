module github.com/arhuman/spproof

// go is the floor a consumer must have; toolchain is what we build and test
// with. They differ on purpose: the floor stays low so the module imports
// widely, the toolchain tracks the latest patch for stdlib security fixes.
go 1.25.0

toolchain go1.26.6

require gopkg.in/yaml.v3 v3.0.1
