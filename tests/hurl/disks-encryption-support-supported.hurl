GET http://localhost/1.0/disks/encryption-support
HTTP 200
[Asserts]
jsonpath "$.metadata.supported" == true
jsonpath "$.metadata.reason_unsupported" not exists
