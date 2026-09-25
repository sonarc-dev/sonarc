package builtin

// maxSymbolFilesForTest sets the symbol cap and returns the previous value.
func maxSymbolFilesForTest(n int) int {
	old := maxSymbolFiles
	maxSymbolFiles = n
	return old
}
