POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '/dev/sdia')",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.dry_run_devices" count == 1
jsonpath "$.metadata.dry_run_devices[0].path" == "/dev/sdia"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '/dev/sdia')",
  "wal_match": "eq(@devnode, '/dev/sdib')",
  "wal_size": "1GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.dry_run_plan" count == 1
jsonpath "$.metadata.dry_run_plan[0].osd_path" == "/dev/sdia"
jsonpath "$.metadata.dry_run_plan[0].wal.kind" == "wal"
jsonpath "$.metadata.dry_run_plan[0].wal.parent_path" == "/dev/sdib"
jsonpath "$.metadata.dry_run_plan[0].wal.partition" == 1
jsonpath "$.metadata.dry_run_plan[0].wal.size" == "1.00 GiB"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '/dev/sdia')",
  "db_match": "eq(@devnode, '/dev/sdic')",
  "db_size": "2GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.dry_run_plan" count == 1
jsonpath "$.metadata.dry_run_plan[0].osd_path" == "/dev/sdia"
jsonpath "$.metadata.dry_run_plan[0].db.kind" == "db"
jsonpath "$.metadata.dry_run_plan[0].db.parent_path" == "/dev/sdic"
jsonpath "$.metadata.dry_run_plan[0].db.partition" == 1
jsonpath "$.metadata.dry_run_plan[0].db.size" == "2.00 GiB"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '/dev/sdia')",
  "wal_match": "eq(@devnode, '/dev/sdib')",
  "wal_size": "1GiB",
  "db_match": "eq(@devnode, '/dev/sdic')",
  "db_size": "2GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.dry_run_plan" count == 1
jsonpath "$.metadata.dry_run_plan[0].osd_path" == "/dev/sdia"
jsonpath "$.metadata.dry_run_plan[0].wal.parent_path" == "/dev/sdib"
jsonpath "$.metadata.dry_run_plan[0].db.parent_path" == "/dev/sdic"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '/dev/sdia')",
  "wal_match": "eq(@devnode, '/dev/sdia')",
  "wal_size": "1GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == "OSD and WAL match sets overlap: /dev/sdia"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '/dev/sdia')",
  "wal_match": "eq(@devnode, '/dev/sdib')",
  "wal_size": "0B",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == "--wal-size must be greater than 0"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '/dev/sdia')",
  "wal_match": "eq(@devnode, '/dev/sdiz')",
  "wal_size": "1GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.warnings" count == 1
jsonpath "$.metadata.warnings[0]" == "WAL match expression resolved to no devices; proceeding without WAL"
jsonpath "$.metadata.dry_run_plan" count == 1
jsonpath "$.metadata.dry_run_plan[0].osd_path" == "/dev/sdia"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '/dev/sdia')",
  "db_match": "eq(@devnode, '/dev/sdiz')",
  "db_size": "1GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.warnings" count == 1
jsonpath "$.metadata.warnings[0]" == "DB match expression resolved to no devices; proceeding without DB"
jsonpath "$.metadata.dry_run_plan" count == 1
jsonpath "$.metadata.dry_run_plan[0].osd_path" == "/dev/sdia"
