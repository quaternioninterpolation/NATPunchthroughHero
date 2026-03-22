#pragma once

#include "Modules/ModuleManager.h"

/** Log category for the NAT Punchthrough plugin. Filter with LogNATPunchthrough in console. */
NATPUNCHTHROUGH_API DECLARE_LOG_CATEGORY_EXTERN(LogNATPunchthrough, Log, All);

class FNATpunchthroughModule : public IModuleInterface
{
public:
	virtual void StartupModule() override;
	virtual void ShutdownModule() override;
};
