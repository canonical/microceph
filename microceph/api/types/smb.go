package types

import "regexp"

// SMBClusterIDRegex is the regular expression for acceptable SMB cluster IDs.
var SMBClusterIDRegex = regexp.MustCompile(`^[\w][\w.-]{1,61}[\w]$`)
