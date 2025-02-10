PUT http://localhost/1.0/ops/maintenance/microceph-0
{
	"Status": "unknown"
}
HTTP 400
[Asserts]
jsonpath "$.error_code" == 400
jsonpath "$.error" == "unknown status encounter: 'unknown', can only be 'maintenance' or 'non-maintenance'"
