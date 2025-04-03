package cluster

// Node represents a Kubernetes node
type Node struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	IP   string `json:"ip"`
}

type Image struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}
