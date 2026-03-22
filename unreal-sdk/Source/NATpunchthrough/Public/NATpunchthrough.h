/**
 * NAT Punchthrough Hero — Convenience Include
 *
 * Include this single header to get access to all plugin types:
 *   #include "NATpunchthrough.h"
 *
 * Core classes:
 *   UNATClient          — High-level component (add to any Actor)
 *   UMasterServerClient — REST API client for the master server
 *   UNATTraversal       — Low-level STUN/UPnP/WebSocket/punch operations
 *
 * Data types & enums are in NATTypes.h.
 */
#pragma once

#include "NATTypes.h"
#include "NATClient.h"
#include "MasterServerClient.h"
#include "NATTraversal.h"
