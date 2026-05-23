// Package procs counts child processes of a parent PID. Read-only.
package procs

// ChildLister returns the number of children of parentPID.
// Used by the service collector to expose vpub_child_count (FR-002).
type ChildLister interface {
	CountChildren(parentPID int) (int, error)
}
