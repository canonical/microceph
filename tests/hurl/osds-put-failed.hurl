PUT http://localhost/1.0/osds
{
	"State": "unknown"
}
HTTP 200
[Asserts]
jsonpath "$.error_code" == 500
jsonpath "$.error" == "unknown state encounter: unknown."
