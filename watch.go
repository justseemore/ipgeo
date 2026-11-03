package ipgeo

import "os"

type fileSig struct {
	size int64
	mod  int64
}

type stamp struct{ v4, v6 fileSig }

func sigOf(path string) (fileSig, error) {
	if path == "" {
		return fileSig{}, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fileSig{}, err
	}
	return fileSig{size: fi.Size(), mod: fi.ModTime().UnixNano()}, nil
}

func (p *Provider) stamp() (stamp, error) {
	v4, err := sigOf(p.v4Path)
	if err != nil {
		return stamp{}, err
	}
	v6, err := sigOf(p.v6Path)
	if err != nil {
		return stamp{}, err
	}
	return stamp{v4: v4, v6: v6}, nil
}
