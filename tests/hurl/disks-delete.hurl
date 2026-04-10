POST http://localhost/1.0/disks
Content-Type: application/json
{
  "path": ["/dev/sdia"]
}
HTTP 200
[Asserts]
jsonpath "$.metadata.report" count == 1
jsonpath "$.metadata.report[0].report" == "Success"
jsonpath "$.metadata.report[0].path" == "/dev/sdia"

GET http://localhost/1.0/disks
HTTP 200
[Captures]
osd_id: jsonpath "$.metadata[0].osd"
[Asserts]
jsonpath "$.metadata" count == 1
jsonpath "$.metadata[0].path" == "/dev/sdia"

DELETE http://localhost/1.0/disks/not-an-int
HTTP 400
[Asserts]
jsonpath "$.error_code" == 400

DELETE http://localhost/1.0/disks/9999
Content-Type: application/json
{
  "osdid": 9999,
  "bypass_safety": true,
  "timeout": 60
}
HTTP 404
[Asserts]
jsonpath "$.error_code" == 404

DELETE http://localhost/1.0/disks/{{osd_id}}
Content-Type: application/json
{
  "osdid": {{osd_id}},
  "bypass_safety": true,
  "timeout": 300
}
HTTP 200

GET http://localhost/1.0/disks
HTTP 200
[Asserts]
jsonpath "$.metadata" count == 0
