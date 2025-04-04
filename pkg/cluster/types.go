package cluster

// Node represents a Kubernetes node
type NodeSpec struct {
	Image    Image `json:"image"`
	MemoryGB int   `json:"memoryGB"`
	CPUCount int   `json:"cpuCount"`
	DiskSize int   `json:"diskSize"`
}

type Image struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	ID     string `json:"id"`
	Size   int    `json:"size"`
}
