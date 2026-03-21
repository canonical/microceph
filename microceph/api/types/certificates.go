package types

// CertificateSetRequest holds the request data for setting RGW SSL certificates.
type CertificateSetRequest struct {
	SSLCertificate string `json:"ssl_certificate" yaml:"ssl_certificate"`
	SSLPrivateKey  string `json:"ssl_private_key" yaml:"ssl_private_key"`
	Restart        bool   `json:"restart" yaml:"restart"`
}
