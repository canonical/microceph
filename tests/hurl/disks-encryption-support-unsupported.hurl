GET http://localhost/1.0/disks/encryption-support
HTTP 200
[Asserts]
jsonpath "$.metadata.supported" == false
jsonpath "$.metadata.reason_unsupported" exists
