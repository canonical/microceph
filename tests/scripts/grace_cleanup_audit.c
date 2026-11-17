/*
 * Observe the real snap helper and librados, without replacing their behavior.
 * Missing shutdown races static destruction (https://github.com/canonical/microceph/issues/899).
 * Fail at exit before that race, making the regression deterministic.
 */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <rados/librados.h>

static rados_t client;
static rados_ioctx_t ioctx;
static bool created;

static void fail(const char *message)
{
	fprintf(stderr, "grace cleanup: %s\n", message);
	_Exit(99);
}

static void *resolve(const char *name)
{
	void *symbol = dlsym(RTLD_NEXT, name);

	if (!symbol)
		fail(dlerror());
	return symbol;
}

static void check_cleanup(void)
{
	if (!created)
		fail("no client creation observed");
	if (ioctx)
		fail("process exited with a live I/O context");
	if (client)
		fail("process exited with a live client");
	fprintf(stderr, "grace cleanup: OK\n");
}

__attribute__((constructor)) static void install_check(void)
{
	/* Registered after librados loads, so this runs before its finalizers. */
	if (atexit(check_cleanup))
		fail("cannot register exit check");
}

int rados_create(rados_t *cluster, const char *id)
{
	int (*real_create)(rados_t *, const char *) = resolve("rados_create");
	int ret = real_create(cluster, id);

	if (ret == 0) {
		if (client)
			fail("client overwritten before shutdown");
		client = *cluster;
		created = true;
	}
	return ret;
}

int rados_ioctx_create(rados_t cluster, const char *pool, rados_ioctx_t *ctx)
{
	int (*real_create)(rados_t, const char *, rados_ioctx_t *) =
		resolve("rados_ioctx_create");
	int ret = real_create(cluster, pool, ctx);

	if (ret == 0) {
		if (ioctx)
			fail("I/O context overwritten before destruction");
		ioctx = *ctx;
	}
	return ret;
}

void rados_ioctx_destroy(rados_ioctx_t ctx)
{
	void (*real_destroy)(rados_ioctx_t) = resolve("rados_ioctx_destroy");

	if (!ctx || ctx != ioctx)
		fail("destroying an unknown I/O context");
	real_destroy(ctx);
	ioctx = NULL;
}

void rados_shutdown(rados_t cluster)
{
	void (*real_shutdown)(rados_t) = resolve("rados_shutdown");

	if (ioctx)
		fail("client shutdown before I/O context destruction");
	if (!cluster || cluster != client)
		fail("shutting down an unknown client");
	real_shutdown(cluster);
	client = NULL;
}
