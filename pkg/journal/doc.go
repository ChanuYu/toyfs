// Package journal implements metadata-only journaling in ordered mode.
//
// All metadata changes are bound to a running transaction. On commit, the
// transaction's dirty data blocks are flushed to their home locations
// (ordered enforcement), then the metadata blocks are written to the journal
// area as four record types: descriptor, metadata, commit, and revocation.
// The escape trick (mask the first 4 bytes when they collide with a record
// magic, restore via descriptor.original_first4) keeps scan unambiguous.
//
// On mount, replay walks the journal in four passes: scan, revocation table,
// replay (with revocation skipping and escape restoration), and finalize.
//
// See docs/07-journal.md for record formats, the commit phase machine, and
// the replay algorithm.
package journal
