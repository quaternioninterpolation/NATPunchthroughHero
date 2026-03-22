#include "NATpunchthroughModule.h"
#include "WebSocketsModule.h"

#define LOCTEXT_NAMESPACE "FNATpunchthroughModule"

DEFINE_LOG_CATEGORY(LogNATPunchthrough);

void FNATpunchthroughModule::StartupModule()
{
	// Ensure WebSockets module is loaded — required on some platforms
	if (!FModuleManager::Get().IsModuleLoaded(TEXT("WebSockets")))
	{
		FModuleManager::Get().LoadModule(TEXT("WebSockets"));
	}
}

void FNATpunchthroughModule::ShutdownModule()
{
}

#undef LOCTEXT_NAMESPACE

IMPLEMENT_MODULE(FNATpunchthroughModule, NATpunchthrough)
