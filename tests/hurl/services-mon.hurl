GET http://localhost/1.0/services/mon 
HTTP 200
[Asserts]
jsonpath "$.metadata.addresses" count == 1
