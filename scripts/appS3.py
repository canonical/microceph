import string
import random
import boto3
import json
import argparse


def app_handle(args):
    keys_path = args.keys
    endpoint = args.endpoint

    # Fetch Auth
    with open(keys_path, 'r') as keys_file:
        keys_dict = json.load(keys_file)

    # Create Boto3 Client
    keys = keys_dict["keys"][0]
    client = boto3.resource("s3", verify=False,
                            endpoint_url=endpoint,
                            aws_access_key_id=keys["access_key"],
                            aws_secret_access_key=keys["secret_key"])

    # Perform IO
    objects = []
    bucket_name = "test-bucket"
    client.Bucket(bucket_name).create()
    for i in range(args.obj_num):
        object_name = "test-object"+rand_str(4)
        data = str(rand_str(random.randint(10, 30)))*1024*1024
        primary_object_one = client.Object(
            bucket_name,
            object_name
        )
        primary_object_one.put(Body=data)
        object_size = primary_object_one.content_length/(1024*1024)
        # Store for cleanup.
        objects.append(
            (object_name, object_size)
        )
        # Print object IO summary:
        print(
            "Object #{}: {}/{} -> Size: {}MB"
            .format(i, bucket_name, object_name, object_size)
        )

    # Print Summary
    print(
        "IO Summary: Object Count {}, Total Size {}MB"
        .format(args.obj_num, sum(size for _, size in objects))
    )

    # Cleanup (if asked for)
    if not args.no_delete:
        print("Performing Cleanup")
        for obj, size in objects:
            client.Object(bucket_name, obj).delete()
        client.Bucket(bucket_name).delete()


def rand_str(length: int):
    return "".join(
        random.choices(string.ascii_uppercase + string.digits, k=length)
    )


if __name__ == "__main__":
    argparse = argparse.ArgumentParser(
        description="An application which uses S3 for storage",
        epilog="Ex: python3 appS3.py <S3 Endpoint> --keys keys.txt",
    )

    argparse.add_argument(
        "endpoint",
        type=str,
        help="Provide RGW endpoint to talk to.",
    )
    argparse.add_argument(
        "keys",
        type=str,
        help="Provide JSON file generated from Ceph RGW Admin.",
    )
    argparse.add_argument(
        "--obj-num",
        type=int,
        default=1,
        help="Number of objects to upload to S3.",
    )
    argparse.add_argument(
        "--no-delete",
        action="store_true",
        help="Setting this to true would not cleanup the pushed objects.",
    )
    argparse.set_defaults(func=app_handle)

    # Parse the args.
    args = argparse.parse_args()

    # Call the subcommand.
    args.func(args)
