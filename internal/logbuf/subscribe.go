package logbuf

// subChanSize is how many chunks a subscriber may fall behind before it
// starts losing them.
const subChanSize = 64

// Subscribe returns a channel of live output chunks and a function that
// unsubscribes and closes it. The returned function is safe to call twice.
func (b *Buffer) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, subChanSize)

	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = ch
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
		b.mu.Unlock()
	}
}

// fanoutLocked copies p to every subscriber, dropping chunks for any
// subscriber that is not keeping up. Never blocks the writer.
func (b *Buffer) fanoutLocked(p []byte) {
	if len(b.subs) == 0 {
		return
	}
	chunk := append([]byte(nil), p...)
	for _, ch := range b.subs {
		select {
		case ch <- chunk:
		default:
		}
	}
}
