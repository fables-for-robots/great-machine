package fleet

// Subscribe returns a channel with room for one value that gets one after
// every Join, Report and Leave that changed something, and a function
// that ends the subscription.
//
// Changes that nobody has read yet are one value: the registry never waits
// for a reader. After the function has returned nothing more is sent; a
// value that was sent before may still be in the channel, which is not
// closed. The function may be called more than once.
func (r *Registry) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.subs, ch)
		r.mu.Unlock()
	}
}

// notify tells every subscriber that something changed. The caller holds
// r.mu.
func (r *Registry) notify() {
	for ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
			// A value waits there already, and says the same.
		}
	}
}
