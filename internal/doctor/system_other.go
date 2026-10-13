//go:build !linux

package doctor

func (NativeHost) ClockSynced() (bool, error)                  { return false, errProbe }
func (NativeHost) Account() (Account, error)                   { return Account{}, errProbe }
func (NativeHost) Properties(Query) (map[string]string, error) { return nil, errProbe }
