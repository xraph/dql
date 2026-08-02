package main

import "sync"

// docs holds the text of every open document.
//
// A language server cannot read files from disk to answer a request: the
// editor's buffer is authoritative and usually differs from what is saved.
// Everything the server answers is computed from what it was told, so this
// store is the server's whole model of the world.
type docs struct {
	mu   sync.RWMutex
	text map[string]string
}

func newDocs() *docs {
	return &docs{text: make(map[string]string)}
}

// set records a document's full text, on open or after a change.
func (d *docs) set(uri, text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.text[uri] = text
}

// get returns a document's text and whether it is open.
//
// Returning ok rather than an empty string matters: a request for a document
// the server has not been told about is a protocol problem worth reporting,
// not a document that happens to be empty.
func (d *docs) get(uri string) (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	t, ok := d.text[uri]
	return t, ok
}

func (d *docs) remove(uri string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.text, uri)
}
