#pragma once

#include "CoreMinimal.h"
#include "Components/ActorComponent.h"
#include "NATTypes.h"
#include "NATClient.generated.h"

class UMasterServerClient;
class UNATTraversal;

DECLARE_DYNAMIC_MULTICAST_DELEGATE_ThreeParams(FOnGameHosted, const FString&, GameId, const FString&, JoinCode, const FString&, HostToken);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnGameJoining, const FString&, GameId);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_TwoParams(FOnNATTypeDetected, ENATType, NATType, const FString&, NATTypeName);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_TwoParams(FOnConnectionMethodDetermined, EConnectionMethod, Method, const FString&, MethodName);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnConnectionEstablished, const FString&, PeerEndpoint);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnClientPeerJoined, const FString&, PeerId);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnClientPeerLeft, const FString&, PeerId);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnClientError, const FString&, Error);
DECLARE_DYNAMIC_MULTICAST_DELEGATE(FOnGameStopped);

/**
 * High-level NAT Punchthrough client component.
 * Add to any Actor to enable NAT traversal networking.
 *
 * Orchestrates MasterServerClient and NATTraversal to provide a simple
 * host/join API with automatic STUN discovery, hole punching, and TURN fallback.
 */
UCLASS(ClassGroup = "NAT Punchthrough", meta = (BlueprintSpawnableComponent))
class NATPUNCHTHROUGH_API UNATClient : public UActorComponent
{
	GENERATED_BODY()

public:
	UNATClient();

	// --- Configuration (set in editor or before hosting/joining) ---

	/** Master server URL (e.g., http://localhost:8080). */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough|Config")
	FString ServerUrl = TEXT("http://localhost:8080");

	/** Optional API key for the master server. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough|Config")
	FString ApiKey;

	/** Attempt UPnP port mapping before STUN. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough|Config")
	bool bTryUPnP = true;

	/** Attempt STUN hole punching. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough|Config")
	bool bTryStunPunch = true;

	/** Fall back to TURN relay if punching fails. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough|Config")
	bool bUseTurnFallback = true;

	/** Timeout for hole punch attempts in seconds. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough|Config", meta = (ClampMin = "3.0", ClampMax = "30.0"))
	float PunchTimeout = 10.0f;

	/** Local game server port. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough|Config")
	int32 GamePort = 7777;

	/** Automatically send heartbeats while hosting. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough|Config")
	bool bAutoHeartbeat = true;

	/** Interval between heartbeats in seconds. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough|Config", meta = (ClampMin = "10.0", ClampMax = "60.0"))
	float HeartbeatInterval = 30.0f;

	// --- Runtime State ---

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|State")
	FString GameId;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|State")
	FString JoinCode;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|State")
	FString HostToken;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|State")
	bool bIsHosting = false;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|State")
	bool bIsClient = false;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|State")
	bool bIsConnected = false;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|State")
	ENATType DetectedNATType = ENATType::Unknown;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|State")
	EConnectionMethod ActiveConnectionMethod = EConnectionMethod::None;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|State")
	FTurnCredentials TurnCredentials;

	// --- Sub-components (accessible for advanced use) ---

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	UMasterServerClient* MasterClient;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	UNATTraversal* Traversal;

	// --- Main API ---

	/**
	 * Host a new game session.
	 * @param GameInfo - Registration info: Name (required), MaxPlayers, Map, Password, GameVersion, etc.
	 */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough")
	void HostGame(const FGameRegistration& GameInfo);

	/**
	 * Join an existing game by join code (6-char) or game ID.
	 * @param Target - Join code or game ID.
	 * @param Password - Optional game password.
	 */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough")
	void JoinGame(const FString& Target, const FString& Password = TEXT(""));

	/** Stop the current game session (host or client). */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough")
	void StopGame();

	/** Get the public game list from the master server. Results via MasterClient->OnGameListReceived. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough")
	void RefreshGameList(const FString& VersionFilter = TEXT(""));

	/** Update the player count on the master server (host only). */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough")
	void UpdatePlayerCount(int32 Count);

	// --- Events ---

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Events")
	FOnGameHosted OnGameHosted;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Events")
	FOnGameJoining OnGameJoining;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Events")
	FOnNATTypeDetected OnNATTypeDetected;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Events")
	FOnConnectionMethodDetermined OnConnectionMethodDetermined;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Events")
	FOnConnectionEstablished OnConnectionEstablished;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Events")
	FOnClientPeerJoined OnPeerJoined;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Events")
	FOnClientPeerLeft OnPeerLeft;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Events")
	FOnClientError OnError;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Events")
	FOnGameStopped OnGameStopped;

protected:
	virtual void BeginPlay() override;
	virtual void EndPlay(const EEndPlayReason::Type EndPlayReason) override;

private:
	FTimerHandle HeartbeatTimerHandle;
	FString PendingPassword;
	FString CurrentSessionId;

	void InitializeSubComponents();
	void StartHeartbeat();
	void StopHeartbeat();
	void SendHeartbeatTick();

	// All handlers bound via AddDynamic MUST be UFUNCTION
	UFUNCTION()
	void HandleGameRegistered(const FRegisterResult& Result);
	UFUNCTION()
	void HandleStunComplete(const FStunResult& Result);
	UFUNCTION()
	void HandleSignalingConnected();
	UFUNCTION()
	void HandleHostRegistered(const FString& RegisteredGameId);
	UFUNCTION()
	void HandleGatherCandidates(const FString& SessionId, const TArray<FString>& StunServers);
	UFUNCTION()
	void HandlePeerCandidate(const FString& SessionId, const FICECandidate& Candidate);
	UFUNCTION()
	void HandlePunchSignal(const FString& SessionId, const FString& PeerIP, int32 PeerPort);
	UFUNCTION()
	void HandleTurnFallback(const FTurnCredentials& Credentials);
	UFUNCTION()
	void HandlePeerConnected(const FString& PeerId, const FString& Method);
	UFUNCTION()
	void HandlePunchComplete(const FPunchResult& Result);
	UFUNCTION()
	void HandleSignalingError(const FString& ErrorMsg);
	UFUNCTION()
	void HandleMasterServerError(const FString& ErrorMsg);
	UFUNCTION()
	void HandleUPnPComplete(const FUPnPResult& Result);

	// Join flow helpers
	UFUNCTION()
	void HandleJoinLookup(const TArray<FGameInfo>& Games);
	void BeginJoinFlow();
	UFUNCTION()
	void HandleTurnCredsForJoin(const FTurnCredentials& Credentials);

	// State for join flow
	bool bJoinPending = false;
	FString PendingJoinTarget;
	FString ResolvedGameId;
};
