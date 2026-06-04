package session

import "github.com/google/uuid"

// newEntryID returns a short uuidv7-derived entry ID. It takes the LAST
// 8 hex digits — the random tail — because the uuidv7 prefix is a
// millisecond timestamp whose top 32 bits are constant for ~18 hours, so a
// prefix slice would collide on nearly every entry in a session. After 100
// collisions against existing IDs it falls back to the full uuid.
func newEntryID(taken func(string) bool) string {
	var last uuid.UUID
	for i := 0; i < 100; i++ {
		u, err := uuid.NewV7()
		if err != nil {
			continue
		}
		last = u
		s := u.String()
		id := s[len(s)-8:]
		if !taken(id) {
			return id
		}
	}
	if last == uuid.Nil {
		// Every NewV7 errored (exhausted entropy source): never emit the
		// zero uuid, which would itself collide on the next fallback.
		return uuid.NewString()
	}
	return last.String()
}

// newSessionID returns a full uuidv7 for session headers.
func newSessionID() string {
	u, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString() // v4 fallback; uniqueness is all we need
	}
	return u.String()
}
