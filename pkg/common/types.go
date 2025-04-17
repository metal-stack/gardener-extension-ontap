package common

// SvmIpaddresses contains the network interface addresses for a Storage Virtual Machine (SVM)
type SvmIpaddresses struct {
	// DataLif is the IP address for data operations
	DataLif string `json:"dataLif,omitempty"`

	// ManagementLif is the IP address for management operations
	ManagementLif string `json:"managementLif,omitempty"`
}

// NetworkTags for SVM network interfaces
const (
	// DataLifTag is the tag used to identify data network interfaces
	DataLifTag = "datalif"

	// ManagementLifTag is the tag used to identify management network interfaces
	ManagementLifTag = "managementlif"
)
