package wire

// maxServiceName is the length of the longest DNS label.
const maxServiceName = 63

// ValidService returns nil for a DNS label, [a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?,
// and a bad-request *Error that quotes the name otherwise.
func ValidService(name string) error {
	if name == "" {
		return Errorf(CodeBadRequest, "%q is no name of a service: it is empty", name)
	}
	if len(name) > maxServiceName {
		return Errorf(CodeBadRequest, "%q is no name of a service: it has %d characters, and %d is the most", name, len(name), maxServiceName)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-':
			if i == 0 || i == len(name)-1 {
				return Errorf(CodeBadRequest, "%q is no name of a service: it begins or ends with a hyphen", name)
			}
		default:
			return Errorf(CodeBadRequest, "%q is no name of a service: only a to z, 0 to 9 and the hyphen are allowed", name)
		}
	}
	return nil
}

// StorePrefix returns what is put before the name of a store path to make
// its reference in the store, for a store path of the service:
// "/services/<service>/nix/store/". Every service has references of its
// own, and the /nix/store of a pod is made of its service's alone. The
// name of a service is a DNS label and needs no escaping; StorePrefix
// does not check that service is one.
func StorePrefix(service string) string {
	return "/services/" + service + "/nix/store/"
}
