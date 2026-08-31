// Package render draws the terminal chrome: rules, navigation rows, the theme
// and the calendar grids.
//
// Most of it is copied from basecamp/hey-cli, which is MIT licensed, and it
// carries 37signals' copyright. The files are kept as close to upstream as
// practical so they stay diffable against it, which is why they hold symbols
// nothing here calls and why they read in a different voice from the rest of
// this repository. Do not tidy them. Behaviour this project needs is added in
// the *_api.go files beside them, and NOTICE lists exactly which files come
// from where.
//
// The directory used to be called heyui, after the client the code came from.
// This project followed HEY's workflow then and no longer does: the screener,
// the Imbox, the Feed and the Paper Trail are gone, and what is left is the
// rendering. The name says what the package does now; NOTICE and this comment
// say where it came from.
package render
