package minion

import (
	"code.google.com/p/go-uuid/uuid"
)

type Minion interface {
	// Returns the unique identifier of a minion
	ID() uuid.UUID

	// Returns the assigned name of the minion
	Name() string

	// Classify a minion with a given classifier
	SetClassifier(c MinionClassifier) error

	// Listens for new tasks and processes them
	TaskListener(c chan<- *MinionTask) error

	// Runs new tasks as received by the TaskListener
	TaskRunner (c <-chan *MinionTask) error

	// Start serving
	Serve() error
}

// Generates a uuid for a minion
func GenerateUUID(name string) uuid.UUID {
	u := uuid.NewSHA1(uuid.NameSpace_DNS, []byte(name))

	return u
}
