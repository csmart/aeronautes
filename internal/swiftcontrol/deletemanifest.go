package swiftcontrol

import "fmt"

// IdentityConflictError is a destructive request refused because the name no
// longer holds what the caller was shown. It is a distinct type so a caller, an
// audit record and a reader of either can tell this apart from a transport
// failure, a missing object or a class refusal (ADR 0085).
type IdentityConflictError struct {
	Container, Name string
	// Expected is the identity the caller supplied; Found is the identity the
	// name holds now, with the fields it was derived from beside it so a reader
	// does not have to own the hashing to understand the refusal.
	Expected, Found string
	FoundClass      string
	FoundLength     int64
	FoundETag       string
}

func (e *IdentityConflictError) Error() string {
	return fmt.Sprintf("%s/%s is not what you were shown: it now reports a %s of %d bytes with entity tag %s, so nothing was deleted (expected identity %s, found %s)",
		e.Container, e.Name, e.FoundClass, e.FoundLength, e.FoundETag, e.Expected, e.Found)
}
