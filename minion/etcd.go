package minion

import (
	"os"
	"os/exec"
	"os/signal"
	"log"
	"bytes"
	"time"
	"strings"
	"strconv"
	"path/filepath"
	"encoding/json"

	"code.google.com/p/go-uuid/uuid"
	"github.com/coreos/etcd/Godeps/_workspace/src/golang.org/x/net/context"
	etcdclient "github.com/coreos/etcd/client"
)

// Minions keyspace in etcd
const etcdMinionSpace = "/gru/minion"

// Etcd Minion
type etcdMinion struct {
	// Name of this minion
	name string

	// Minion root node in etcd 
	rootDir string

	// Minion queue node in etcd
	queueDir string

	// Log directory to keep previously executed tasks
	logDir string

	// Root node for classifiers in etcd
	classifierDir string

	// Minion unique identifier
	id uuid.UUID

	// KeysAPI client to etcd
	kapi etcdclient.KeysAPI
}

// Creates a new etcd minion
func NewEtcdMinion(name string, cfg etcdclient.Config) Minion {
	c, err := etcdclient.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	kapi := etcdclient.NewKeysAPI(c)
	id := GenerateUUID(name)
	rootDir := filepath.Join(etcdMinionSpace, id.String())
	queueDir := filepath.Join(rootDir, "queue")
	classifierDir := filepath.Join(rootDir, "classifier")
	logDir := filepath.Join(rootDir, "log")

	m := &etcdMinion{
		name: name,
		rootDir: rootDir,
		queueDir: queueDir,
		classifierDir: classifierDir,
		logDir: logDir,
		id: id,
		kapi: kapi,
	}

	return m
}

// Set the human-readable name of the minion in etcd
func (m *etcdMinion) setName() error {
	nameKey := filepath.Join(m.rootDir, "name")
	opts := &etcdclient.SetOptions{
		PrevExist: etcdclient.PrevIgnore,
	}

	_, err := m.kapi.Set(context.Background(), nameKey, m.name, opts)

	return err
}

// Set the time the minion was last seen in seconds since the Epoch
func (m *etcdMinion) setLastseen(s int64) error {
	lastseenKey := filepath.Join(m.rootDir, "lastseen")
	lastseenValue := strconv.FormatInt(s, 10)
	opts := &etcdclient.SetOptions{
		PrevExist: etcdclient.PrevIgnore,
	}

	_, err := m.kapi.Set(context.Background(), lastseenKey, lastseenValue, opts)

	return err
}

// Checks for any tasks pending tasks in queue
func (m *etcdMinion) checkQueue(c chan<- *MinionTask) error {
	opts := &etcdclient.GetOptions{
		Recursive: true,
		Sort: true,
	}

	// Get backlog tasks if any
	resp, err := m.kapi.Get(context.Background(), m.queueDir, opts)
	if err != nil {
		return nil
	}

	backlog := resp.Node.Nodes
	if len(backlog) == 0 {
		// No backlog tasks found
		return nil
	}

	log.Printf("Found %d tasks in backlog", len(backlog))
	for _, node := range backlog {
		task, err := EtcdUnmarshalTask(node)
		m.kapi.Delete(context.Background(), node.Key, nil)

		if err != nil {
			continue
		}

		c <- task
	}

	return nil
}

// Runs periodic jobs such as refreshing classifiers and updating lastseen
func (m *etcdMinion) periodicRunner(ticker *time.Ticker) error {
	for {
		// Update classifiers
		for _, classifier := range ClassifierRegistry {
			m.SetClassifier(classifier)
		}

		// Update lastseen time
		now := time.Now().Unix()
		err := m.setLastseen(now)
		if err != nil {
			log.Printf("Failed to update lastseen time: %s\n", err)
		}

		<- ticker.C
	}

	return nil
}

// Processes new tasks
func (m *etcdMinion) processTask(t *MinionTask) error {
	defer m.saveTask(t)

	var buf bytes.Buffer
	cmd := exec.Command(t.Command, t.Args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	log.Printf("Processing task %s\n", t.TaskID)

	cmdError := cmd.Run()
	t.TimeProcessed = time.Now().Unix()
	t.Result = buf.String()

	if cmdError != nil {
		log.Printf("Failed to process task %s\n", t.TaskID)
		t.Error = cmdError.Error()
	} else {
		log.Printf("Finished processing task %s\n", t.TaskID)
	}

	return cmdError
}

// Saves a task in the minion's log
func (m *etcdMinion) saveTask(t *MinionTask) error {
	// Task key in etcd
	taskKey := filepath.Join(m.logDir, t.TaskID.String())

	// Serialize task to JSON
	data, err := json.Marshal(t)
	if err != nil {
		log.Printf("Failed to serialize task %s: %s\n", t.TaskID, err)
		return err
	}

	// Save task result in the minion's space
	opts := &etcdclient.SetOptions{
		PrevExist: etcdclient.PrevIgnore,
	}
	_, err = m.kapi.Set(context.Background(), taskKey, string(data), opts)
	if err != nil {
		log.Printf("Failed to save task %s: %s\n", t.TaskID, err)
		return err
	}

	return err
}

// Unmarshals task from etcd
func EtcdUnmarshalTask(node *etcdclient.Node) (*MinionTask, error) {
	task := new(MinionTask)
	err := json.Unmarshal([]byte(node.Value), &task)

	if err != nil {
		log.Printf("Invalid task %s: %s\n", node.Key, err)
	}

	return task, err
}

// Returns the minion unique identifier
func (m *etcdMinion) ID() uuid.UUID {
	return m.id
}

// Returns the assigned name of the minion
func (m *etcdMinion) Name() string {
	return m.name
}

// Classify a minion with a given key and value
func (m *etcdMinion) SetClassifier(c MinionClassifier) error {
	// Classifiers in etcd expire after an hour
	opts := &etcdclient.SetOptions{
		PrevExist: etcdclient.PrevIgnore,
		TTL: time.Hour,
	}

	// Get classifier values
	key, err := c.GetKey()
	description, err := c.GetDescription()
	value, err := c.GetValue()

	if err != nil {
		return err
	}

	// Create a simple classifier and serialize to JSON
	klassifier := NewSimpleClassifier(key, description, value)
	data, err := json.Marshal(klassifier)

	if err != nil {
		log.Printf("Failed to serialize classifier: %s\n", key)
		return err
	}

	// Set minion classifier in etcd
	klassifierKey := filepath.Join(m.classifierDir, key)
	_, err = m.kapi.Set(context.Background(), klassifierKey, string(data), opts)

	if err != nil {
		log.Printf("Failed to set classifier %s: %s\n", key, err)
	}

	return err
}

// Monitors etcd for new tasks for processing
func (m *etcdMinion) TaskListener(c chan<- *MinionTask) error {
	watcherOpts := &etcdclient.WatcherOptions{
		Recursive: true,
	}
	watcher := m.kapi.Watcher(m.queueDir, watcherOpts)

	for {
		resp, err := watcher.Next(context.Background())
		if err != nil {
			log.Printf("Failed to read task: %s\n", err)
			continue
		}

		// Ignore "delete" events when removing a task from the queue
		action := strings.ToLower(resp.Action)
		if strings.EqualFold(action, "delete") {
			continue
		}

		// Remove task from the queue
		task, err := EtcdUnmarshalTask(resp.Node)
		m.kapi.Delete(context.Background(), resp.Node.Key, nil)

		if err != nil {
			continue
		}

		log.Printf("Received task %s\n", task.TaskID)

		c <- task
	}

	return nil
}

// Processes new tasks
func (m *etcdMinion) TaskRunner(c <-chan *MinionTask) error {
	for {
		task := <-c

		task.TimeReceived = time.Now().Unix()

		if task.IsConcurrent {
			go m.processTask(task)
		} else {
			m.processTask(task)
		}
	}

	return nil
}

// Main entry point of the minion
func (m *etcdMinion) Serve() error {
	// Channel on which we send the quit signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)

	// Initialize minion
	m.setName()

	log.Printf("Minion %s is ready to serve", m.id)

	// Run periodic tasks every fifteen minutes
	ticker := time.NewTicker(time.Minute * 15)
	go m.periodicRunner(ticker)

	// Check for pending tasks in queue
	tasks := make(chan *MinionTask)
	m.checkQueue(tasks)

	go m.TaskListener(tasks)
	go m.TaskRunner(tasks)

	// Block until a stop signal is received
	s := <-quit
	log.Printf("Received %s signal, shutting down", s)
	close(quit)
	close(tasks)

	return nil
}
