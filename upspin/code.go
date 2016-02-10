package upspin

// This file contains implementations of things like marshaling of the
// basic Upspin types.

// Marshal packs the Location into a byte slice for transport.
func (Location) Marshal([]byte) error {
	panic("unimplemented")
}

// Unmarshal unpacks the byte slice to recover the encoded Location.
func (Location) Unmarshal([]byte) error {
	panic("unimplemented")
}

// Marshal packs the Reference into a byte slice for transport.
func (Reference) Marshal([]byte) error {
	panic("unimplemented")
}

// Unmarshal unpacks the byte slice to recover the encoded Reference.
func (Reference) Unmarshal([]byte) error {
	panic("unimplemented")
}
