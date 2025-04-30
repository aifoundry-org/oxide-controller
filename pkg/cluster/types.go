package cluster

// Node represents a Kubernetes node
type NodeSpec struct {
	Image         Image `json:"image"`
	MemoryGB      int   `json:"memoryGB"`
	CPUCount      int   `json:"cpuCount"`
	RootDiskSize  int   `json:"diskSize"`
	ExtraDiskSize int   `json:"extraDiskSize"`
	ExternalIP    bool  `json:"externalIP"`
}

type Image struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Blocksize int    `json:"blocksize"`
	ID        string `json:"id"`
	Size      int    `json:"size"`
}
