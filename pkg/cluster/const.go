package cluster

const (
	KB = 1024
	MB = 1024 * KB
	GB = 1024 * MB

	blockSize = 4096

	secretKeyUserSSH          = "user-ssh-public-key"
	secretKeyJoinToken        = "k3s-join-token"
	secretKeySystemSSHPublic  = "system-ssh-public-key"
	secretKeySystemSSHPrivate = "system-ssh-private-key"
	secretKeyWorkerCount      = "worker-count"
	secretKeyOxideToken       = "oxide-token"
	secretKeyOxideURL         = "oxide-url"
	maximumChunkSize          = 512 * KB

	devModeOCIImage  = "dev"
	utilityImageName = "alpine:3.21"
)
