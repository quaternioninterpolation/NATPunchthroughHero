#pragma once

#include "CoreMinimal.h"
#include "NATTypes.h"
#include "Interfaces/IHttpRequest.h"
#include "Interfaces/IHttpResponse.h"
#include "MasterServerClient.generated.h"

DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnGameRegistered, const FRegisterResult&, Result);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnGameListReceived, const TArray<FGameInfo>&, Games);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnGameInfoReceived, const FGameInfo&, GameInfo);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnTurnCredentialsReceived, const FTurnCredentials&, Credentials);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnHealthCheckComplete, const FServerHealth&, Health);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnMasterServerError, const FString&, Error);
DECLARE_DYNAMIC_MULTICAST_DELEGATE(FOnHeartbeatSent);
DECLARE_DYNAMIC_MULTICAST_DELEGATE(FOnGameDeregistered);

/**
 * REST client for the NAT Punchthrough Hero master server.
 * Handles game registration, listing, heartbeat, TURN credentials, and health checks.
 */
UCLASS(BlueprintType, Blueprintable, ClassGroup = "NAT Punchthrough")
class NATPUNCHTHROUGH_API UMasterServerClient : public UObject
{
	GENERATED_BODY()

public:
	UMasterServerClient();

	/** Initialize with server URL and optional API key. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Master Server")
	void Initialize(const FString& InServerUrl, const FString& InApiKey = TEXT(""));

	// --- Game Management ---

	/** Register a new game session on the master server. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Master Server")
	void RegisterGame(const FGameRegistration& Registration);

	/** List public games, optionally filtered by join code or version. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Master Server")
	void ListGames(const FString& Code = TEXT(""), const FString& Version = TEXT(""), int32 Limit = 50, int32 Offset = 0);

	/** Get info for a specific game by ID. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Master Server")
	void GetGame(const FString& GameId);

	/** Send a heartbeat to keep the game session alive. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Master Server")
	void SendHeartbeat(const FString& GameId, const FString& HostToken);

	/** Update the current player count for a hosted game. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Master Server")
	void UpdatePlayerCount(const FString& GameId, const FString& HostToken, int32 CurrentPlayers);

	/** Remove a game session from the master server. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Master Server")
	void DeregisterGame(const FString& GameId, const FString& HostToken);

	// --- TURN ---

	/** Request TURN relay credentials for a game. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Master Server")
	void GetTurnCredentials(const FString& GameId);

	// --- Health ---

	/** Check the master server health status. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Master Server")
	void CheckHealth();

	// --- Events ---

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Master Server")
	FOnGameRegistered OnGameRegistered;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Master Server")
	FOnGameListReceived OnGameListReceived;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Master Server")
	FOnGameInfoReceived OnGameInfoReceived;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Master Server")
	FOnTurnCredentialsReceived OnTurnCredentialsReceived;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Master Server")
	FOnHealthCheckComplete OnHealthCheckComplete;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Master Server")
	FOnHeartbeatSent OnHeartbeatSent;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Master Server")
	FOnGameDeregistered OnGameDeregistered;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Master Server")
	FOnMasterServerError OnError;

private:
	FString ServerUrl;
	FString ApiKey;

	TSharedRef<class IHttpRequest> CreateRequest(const FString& Endpoint, const FString& Verb) const;
	void AddAuthHeaders(TSharedRef<IHttpRequest>& Request, const FString& HostToken = TEXT("")) const;

	void HandleRegisterResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess);
	void HandleListResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess);
	void HandleGetGameResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess);
	void HandleHeartbeatResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess);
	void HandleDeregisterResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess);
	void HandleUpdatePlayerCountResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess);
	void HandleTurnResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess);
	void HandleHealthResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess);

	FGameInfo ParseGameInfo(const TSharedPtr<FJsonObject>& JsonObj) const;
};
