GET http://localhost/1.0/disks
HTTP 200
[Asserts]
jsonpath "$.metadata" count == 0
