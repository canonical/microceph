package common

// This file previously contained GetClusterMemberNames which used the v2
// microcluster API. In v3, cluster member access is done differently through
// the microcluster client's GetClusterMembers method or state.Cluster().
// The function was unused and has been removed during the v2 to v3 migration.
