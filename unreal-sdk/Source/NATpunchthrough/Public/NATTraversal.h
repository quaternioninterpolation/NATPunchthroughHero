#pragma once

#include "CoreMinimal.h"
#include "NATTypes.h"
#include "IWebSocket.h"
#include "NATTraversal.generated.h"

DECLARE_DYNAMIC_MULTICAST_DELEGATE(FOnSignalingConnected);
DECLARE_DYNAMIC_MULTICAST_DELEGATE(FOnSignalingDisconnected);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnHostRegistered, const FString&, GameId);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_TwoParams(FOnGatherCandidates, const FString&, SessionId, const TArray<FString>&, StunServers);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_TwoParams(FOnPeerCandidate, const FString&, SessionId, const FICECandidate&, Candidate);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_ThreeParams(FOnPunchSignal, const FString&, SessionId, const FString&, PeerIP, int32, PeerPort);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnTurnFallback, const FTurnCredentials&, Credentials);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_TwoParams(FOnPeerConnected, const FString&, PeerId, const FString&, Method);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnSignalingError, const FString&, Error);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnStunComplete, const FStunResult&, Result);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnUPnPComplete, const FUPnPResult&, Result);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FOnPunchComplete, const FPunchResult&, Result);

/**
 * Low-level NAT traversal operations: STUN discovery, UPnP port mapping,
 * WebSocket signaling, and UDP hole punching.
 */
UCLASS(BlueprintType, Blueprintable, ClassGroup = "NAT Punchthrough")
class NATPUNCHTHROUGH_API UNATTraversal : public UObject
{
	GENERATED_BODY()

public:
	UNATTraversal();
	virtual void BeginDestroy() override;

	/** Set the owning world so timers work correctly. Must be called before AttemptPunch. */
	void SetWorld(UWorld* InWorld) { OwningWorld = InWorld; }

	// --- STUN Discovery ---

	/**
	 * Discover public IP and NAT type via STUN.
	 * Result returned via OnStunDiscoveryComplete delegate (bind before calling).
	 */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void DiscoverNAT(const FString& StunServer = TEXT("stun.l.google.com"), int32 StunPort = 19302);

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|Traversal")
	FString PublicIP;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|Traversal")
	int32 PublicPort = 0;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|Traversal")
	ENATType DetectedNATType = ENATType::Unknown;

	// --- UPnP Port Mapping ---

	/**
	 * Attempt UPnP port mapping for the given port.
	 * Performs SSDP discovery and SOAP AddPortMapping.
	 * Result returned via OnUPnPComplete delegate.
	 */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void TryUPnP(int32 Port, int32 TimeoutMs = 5000);

	/** Release a previously created UPnP port mapping. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void ReleaseUPnP(int32 Port);

	// --- WebSocket Signaling ---

	/** Connect to the signaling WebSocket server. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void ConnectSignaling(const FString& Url, const FString& InApiKey = TEXT(""));

	/** Disconnect from the signaling server. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void DisconnectSignaling();

	/** Register as a host on the signaling server. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void RegisterHost(const FString& GameId, const FString& HostToken);

	/** Request to join a game via signaling. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void RequestJoin(const FString& GameId, const FString& JoinCode = TEXT(""), const FString& Password = TEXT(""));

	/** Send an ICE candidate to the signaling server. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void SendICECandidate(const FString& SessionId, const FICECandidate& Candidate);

	/** Notify the server that a connection has been established. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void SendConnectionEstablished(const FString& SessionId, EConnectionMethod Method);

	/** Send a heartbeat ping over WebSocket. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void SendHeartbeat();

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|Traversal")
	bool bIsSignalingConnected = false;

	// --- UDP Hole Punch ---

	/**
	 * Attempt a UDP hole punch to the given peer endpoint.
	 * Sends probe packets and listens for a response.
	 */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void AttemptPunch(const FString& PeerIP, int32 PeerPort, int32 LocalPort, float TimeoutSeconds = 10.0f);

	/** Stop an ongoing punch attempt. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough|Traversal")
	void StopPunch();

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough|Traversal")
	EConnectionMethod ActiveConnectionMethod = EConnectionMethod::None;

	// --- Signaling Events ---

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnSignalingConnected OnSignalingConnected;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnSignalingDisconnected OnSignalingDisconnected;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnHostRegistered OnHostRegistered;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnGatherCandidates OnGatherCandidates;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnPeerCandidate OnPeerCandidate;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnPunchSignal OnPunchSignal;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnTurnFallback OnTurnFallback;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnPeerConnected OnPeerConnected;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnSignalingError OnSignalingError;

	// --- Result Events ---

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnStunComplete OnStunDiscoveryComplete;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnUPnPComplete OnUPnPComplete;

	UPROPERTY(BlueprintAssignable, Category = "NAT Punchthrough|Traversal")
	FOnPunchComplete OnPunchComplete;

private:
	// World reference for timers (set by NATClient from its owning actor)
	TWeakObjectPtr<UWorld> OwningWorld;

	// WebSocket
	TSharedPtr<IWebSocket> WebSocket;
	void OnWebSocketConnected();
	void OnWebSocketConnectionError(const FString& Error);
	void OnWebSocketClosed(int32 StatusCode, const FString& Reason, bool bWasClean);
	void OnWebSocketMessage(const FString& Message);
	void SendSignalingMessage(const TSharedPtr<FJsonObject>& Msg);

	// STUN internals
	void PerformStunBinding(const FString& StunServer, int32 StunPort);
	static TArray<uint8> BuildStunBindingRequest(TArray<uint8>& OutTransactionId);
	static bool ParseStunBindingResponse(const TArray<uint8>& Data, const TArray<uint8>& TransactionId, FString& OutIP, int32& OutPort);

	// UPnP internals
	int32 MappedUPnPPort = 0;
	FString UPnPControlUrl;
	void PerformUPnPMapping(int32 Port, int32 TimeoutMs);
	static FString BuildSOAPAddPortMapping(int32 Port, const FString& LocalIP);
	static FString BuildSOAPDeletePortMapping(int32 Port);

	// Punch internals
	FSocket* PunchSocket = nullptr;
	FTimerHandle PunchTimerHandle;
	FTimerHandle PunchTimeoutHandle;
	bool bPunching = false;
	FString PunchTargetIP;
	int32 PunchTargetPort = 0;
	void PunchTick();
};
