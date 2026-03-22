#include "NATClient.h"
#include "NATpunchthroughModule.h"
#include "MasterServerClient.h"
#include "NATTraversal.h"
#include "SocketSubsystem.h"
#include "IPAddress.h"
#include "TimerManager.h"
#include "Engine/World.h"

UNATClient::UNATClient()
{
	PrimaryComponentTick.bCanEverTick = false;
}

void UNATClient::BeginPlay()
{
	Super::BeginPlay();
	InitializeSubComponents();
}

void UNATClient::EndPlay(const EEndPlayReason::Type EndPlayReason)
{
	StopGame();
	Super::EndPlay(EndPlayReason);
}

void UNATClient::InitializeSubComponents()
{
	// Create MasterServerClient
	MasterClient = NewObject<UMasterServerClient>(this);
	MasterClient->Initialize(ServerUrl, ApiKey);

	// Bind master server events
	MasterClient->OnGameRegistered.AddDynamic(this, &UNATClient::HandleGameRegistered);
	MasterClient->OnError.AddDynamic(this, &UNATClient::HandleMasterServerError);

	// Create NATTraversal and give it our world for timers
	Traversal = NewObject<UNATTraversal>(this);
	Traversal->SetWorld(GetWorld());

	// Bind traversal events
	Traversal->OnStunDiscoveryComplete.AddDynamic(this, &UNATClient::HandleStunComplete);
	Traversal->OnSignalingConnected.AddDynamic(this, &UNATClient::HandleSignalingConnected);
	Traversal->OnHostRegistered.AddDynamic(this, &UNATClient::HandleHostRegistered);
	Traversal->OnGatherCandidates.AddDynamic(this, &UNATClient::HandleGatherCandidates);
	Traversal->OnPeerCandidate.AddDynamic(this, &UNATClient::HandlePeerCandidate);
	Traversal->OnPunchSignal.AddDynamic(this, &UNATClient::HandlePunchSignal);
	Traversal->OnTurnFallback.AddDynamic(this, &UNATClient::HandleTurnFallback);
	Traversal->OnPeerConnected.AddDynamic(this, &UNATClient::HandlePeerConnected);
	Traversal->OnPunchComplete.AddDynamic(this, &UNATClient::HandlePunchComplete);
	Traversal->OnSignalingError.AddDynamic(this, &UNATClient::HandleSignalingError);
	Traversal->OnUPnPComplete.AddDynamic(this, &UNATClient::HandleUPnPComplete);
}

// =============================================================================
// Host Game
// =============================================================================

void UNATClient::HostGame(const FGameRegistration& GameInfo)
{
	if (bIsHosting || bIsClient)
	{
		OnError.Broadcast(TEXT("Already in a game session. Call StopGame() first."));
		return;
	}

	if (!MasterClient || !Traversal)
	{
		OnError.Broadcast(TEXT("NATClient not initialized. Ensure the owning Actor has called BeginPlay."));
		return;
	}

	bIsHosting = true;
	bIsClient = false;
	bJoinPending = false;

	UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Hosting game '%s'..."), *GameInfo.Name);

	// Re-initialize master client with current settings
	MasterClient->Initialize(ServerUrl, ApiKey);

	// Register the game on the master server
	FGameRegistration Registration = GameInfo;
	Registration.HostPort = GamePort;

	// Attempt UPnP port mapping for direct connectivity
	if (bTryUPnP)
	{
		Traversal->TryUPnP(GamePort);
	}

	// Run STUN discovery to get our NAT type
	if (bTryStunPunch)
	{
		Traversal->DiscoverNAT();
	}

	MasterClient->RegisterGame(Registration);
}

void UNATClient::HandleGameRegistered(const FRegisterResult& Result)
{
	if (!Result.bSuccess)
	{
		bIsHosting = false;
		OnError.Broadcast(FString::Printf(TEXT("Failed to register game: %s"), *Result.Error));
		return;
	}

	GameId = Result.GameId;
	JoinCode = Result.JoinCode;
	HostToken = Result.HostToken;

	UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Game registered - ID: %s, Code: %s"), *GameId, *JoinCode);

	// Connect to signaling server
	Traversal->ConnectSignaling(ServerUrl, ApiKey);

	// Start heartbeat
	if (bAutoHeartbeat)
	{
		StartHeartbeat();
	}

	OnGameHosted.Broadcast(GameId, JoinCode, HostToken);
}

void UNATClient::HandleSignalingConnected()
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Signaling connected"));

	if (bIsHosting)
	{
		Traversal->RegisterHost(GameId, HostToken);
	}
	else if (bJoinPending)
	{
		// Only pass the join code if the original target was a 6-char code, not a game ID
		FString JoinCodeToSend = (PendingJoinTarget.Len() == 6) ? PendingJoinTarget : TEXT("");
		Traversal->RequestJoin(ResolvedGameId, JoinCodeToSend, PendingPassword);
	}
}

void UNATClient::HandleHostRegistered(const FString& RegisteredGameId)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Host registered on signaling server for game %s"), *RegisteredGameId);
}

// =============================================================================
// Join Game
// =============================================================================

void UNATClient::JoinGame(const FString& Target, const FString& Password)
{
	if (bIsHosting || bIsClient)
	{
		OnError.Broadcast(TEXT("Already in a game session. Call StopGame() first."));
		return;
	}

	if (!MasterClient || !Traversal)
	{
		OnError.Broadcast(TEXT("NATClient not initialized. Ensure the owning Actor has called BeginPlay."));
		return;
	}

	bIsClient = true;
	bIsHosting = false;
	bJoinPending = true;
	PendingPassword = Password;
	PendingJoinTarget = Target;

	UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Joining game '%s'..."), *Target);

	MasterClient->Initialize(ServerUrl, ApiKey);

	// Determine if target is a join code (6 chars) or game ID
	if (Target.Len() == 6)
	{
		MasterClient->OnGameListReceived.AddDynamic(this, &UNATClient::HandleJoinLookup);
		MasterClient->ListGames(Target);
	}
	else
	{
		ResolvedGameId = Target;
		OnGameJoining.Broadcast(ResolvedGameId);
		BeginJoinFlow();
	}
}

void UNATClient::HandleJoinLookup(const TArray<FGameInfo>& Games)
{
	MasterClient->OnGameListReceived.RemoveDynamic(this, &UNATClient::HandleJoinLookup);

	if (Games.Num() == 0)
	{
		bIsClient = false;
		bJoinPending = false;
		OnError.Broadcast(TEXT("Game not found with that join code"));
		return;
	}

	ResolvedGameId = Games[0].Id;
	OnGameJoining.Broadcast(ResolvedGameId);
	BeginJoinFlow();
}

void UNATClient::BeginJoinFlow()
{
	// Get TURN credentials
	MasterClient->OnTurnCredentialsReceived.AddDynamic(this, &UNATClient::HandleTurnCredsForJoin);
	MasterClient->GetTurnCredentials(ResolvedGameId);

	// Run STUN discovery
	if (bTryStunPunch)
	{
		Traversal->DiscoverNAT();
	}

	// Connect to signaling
	Traversal->ConnectSignaling(ServerUrl, ApiKey);
}

void UNATClient::HandleTurnCredsForJoin(const FTurnCredentials& Credentials)
{
	MasterClient->OnTurnCredentialsReceived.RemoveDynamic(this, &UNATClient::HandleTurnCredsForJoin);
	TurnCredentials = Credentials;
	UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: TURN credentials received"));
}

// =============================================================================
// NAT Traversal Flow
// =============================================================================

void UNATClient::HandleStunComplete(const FStunResult& Result)
{
	if (Result.bSuccess)
	{
		DetectedNATType = Result.NATType;
		UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: STUN discovery - Public IP: %s:%d, NAT: %s"),
			*Result.PublicIP, Result.PublicPort, *NATTypeToString(Result.NATType));
		OnNATTypeDetected.Broadcast(Result.NATType, NATTypeToString(Result.NATType));
	}
	else
	{
		UE_LOG(LogNATPunchthrough, Warning, TEXT("NATClient: STUN discovery failed: %s"), *Result.Error);
	}
}

void UNATClient::HandleGatherCandidates(const FString& SessionId, const TArray<FString>& StunServers)
{
	CurrentSessionId = SessionId;
	UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Gathering ICE candidates for session %s"), *SessionId);

	// Send our discovered public endpoint as an ICE candidate
	if (Traversal && !Traversal->PublicIP.IsEmpty())
	{
		FICECandidate Candidate;
		Candidate.PublicIP = Traversal->PublicIP;
		Candidate.PublicPort = Traversal->PublicPort;

		// Get local IP
		ISocketSubsystem* SocketSub = ISocketSubsystem::Get(PLATFORM_SOCKETSUBSYSTEM);
		if (SocketSub)
		{
			bool bCanBindAll;
			TSharedPtr<FInternetAddr> LocalAddr = SocketSub->GetLocalHostAddr(*GLog, bCanBindAll);
			if (LocalAddr.IsValid())
			{
				Candidate.LocalIP = LocalAddr->ToString(false);
				Candidate.LocalPort = GamePort;
			}
		}

		Candidate.NATTypeString = NATTypeToString(DetectedNATType);
		Traversal->SendICECandidate(SessionId, Candidate);
	}
}

void UNATClient::HandlePeerCandidate(const FString& SessionId, const FICECandidate& Candidate)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Peer candidate received - %s:%d (NAT: %s)"),
		*Candidate.PublicIP, Candidate.PublicPort, *Candidate.NATTypeString);
}

void UNATClient::HandlePunchSignal(const FString& SessionId, const FString& PeerIP, int32 PeerPort)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Punch signal - target %s:%d"), *PeerIP, PeerPort);

	if (bTryStunPunch && Traversal)
	{
		Traversal->AttemptPunch(PeerIP, PeerPort, GamePort, PunchTimeout);
	}
}

void UNATClient::HandlePunchComplete(const FPunchResult& Result)
{
	if (Result.bSuccess)
	{
		UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Hole punch succeeded! Remote: %s"), *Result.RemoteEndpoint);
		ActiveConnectionMethod = EConnectionMethod::StunPunch;
		bIsConnected = true;
		OnConnectionMethodDetermined.Broadcast(EConnectionMethod::StunPunch, TEXT("STUN Punch"));
		OnConnectionEstablished.Broadcast(Result.RemoteEndpoint);

		if (Traversal)
		{
			Traversal->SendConnectionEstablished(CurrentSessionId, EConnectionMethod::StunPunch);
		}
	}
	else
	{
		UE_LOG(LogNATPunchthrough, Warning, TEXT("NATClient: Hole punch failed: %s"), *Result.Error);

		if (bUseTurnFallback)
		{
			UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Waiting for TURN fallback from server..."));
		}
		else
		{
			OnError.Broadcast(TEXT("Hole punch failed and TURN fallback is disabled"));
		}
	}
}

void UNATClient::HandleTurnFallback(const FTurnCredentials& Credentials)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: TURN fallback received - relay via %s"), *Credentials.Username);
	TurnCredentials = Credentials;
	ActiveConnectionMethod = EConnectionMethod::TurnRelay;
	bIsConnected = true;
	OnConnectionMethodDetermined.Broadcast(EConnectionMethod::TurnRelay, TEXT("TURN Relay"));
	OnConnectionEstablished.Broadcast(TEXT("relay"));

	if (Traversal)
	{
		Traversal->SendConnectionEstablished(CurrentSessionId, EConnectionMethod::TurnRelay);
	}
}

void UNATClient::HandlePeerConnected(const FString& PeerId, const FString& Method)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Peer connected - %s via %s"), *PeerId, *Method);
	OnPeerJoined.Broadcast(PeerId);
}

// =============================================================================
// Stop Game
// =============================================================================

void UNATClient::StopGame()
{
	StopHeartbeat();

	if (MasterClient && bIsHosting && !GameId.IsEmpty() && !HostToken.IsEmpty())
	{
		MasterClient->DeregisterGame(GameId, HostToken);
	}

	if (Traversal)
	{
		Traversal->StopPunch();
		Traversal->DisconnectSignaling();
		Traversal->ReleaseUPnP(GamePort);
	}

	GameId.Empty();
	JoinCode.Empty();
	HostToken.Empty();
	CurrentSessionId.Empty();
	bIsHosting = false;
	bIsClient = false;
	bIsConnected = false;
	bJoinPending = false;
	ActiveConnectionMethod = EConnectionMethod::None;
	DetectedNATType = ENATType::Unknown;

	OnGameStopped.Broadcast();
}

// =============================================================================
// Game List & Player Count
// =============================================================================

void UNATClient::RefreshGameList(const FString& VersionFilter)
{
	if (MasterClient)
	{
		MasterClient->ListGames(TEXT(""), VersionFilter);
	}
}

void UNATClient::UpdatePlayerCount(int32 Count)
{
	if (!bIsHosting)
	{
		UE_LOG(LogNATPunchthrough, Warning, TEXT("NATClient: Cannot update player count - not hosting"));
		return;
	}

	if (MasterClient && !GameId.IsEmpty() && !HostToken.IsEmpty())
	{
		MasterClient->UpdatePlayerCount(GameId, HostToken, Count);
		UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: Player count updated to %d"), Count);
	}
}

// =============================================================================
// Heartbeat
// =============================================================================

void UNATClient::StartHeartbeat()
{
	if (UWorld* World = GetWorld())
	{
		World->GetTimerManager().SetTimer(
			HeartbeatTimerHandle,
			this,
			&UNATClient::SendHeartbeatTick,
			HeartbeatInterval,
			true
		);
	}
}

void UNATClient::StopHeartbeat()
{
	if (HeartbeatTimerHandle.IsValid())
	{
		if (UWorld* World = GetWorld())
		{
			World->GetTimerManager().ClearTimer(HeartbeatTimerHandle);
		}
	}
}

void UNATClient::SendHeartbeatTick()
{
	if (bIsHosting && !GameId.IsEmpty() && !HostToken.IsEmpty())
	{
		if (MasterClient)
		{
			MasterClient->SendHeartbeat(GameId, HostToken);
		}

		if (Traversal && Traversal->bIsSignalingConnected)
		{
			Traversal->SendHeartbeat();
		}
	}
}

// =============================================================================
// Error Handlers
// =============================================================================

void UNATClient::HandleSignalingError(const FString& ErrorMsg)
{
	UE_LOG(LogNATPunchthrough, Error, TEXT("NATClient: Signaling error: %s"), *ErrorMsg);
	OnError.Broadcast(ErrorMsg);
}

void UNATClient::HandleUPnPComplete(const FUPnPResult& Result)
{
	if (Result.bSuccess)
	{
		UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: UPnP port %d mapped successfully"), Result.ExternalPort);
		ActiveConnectionMethod = EConnectionMethod::Direct;
		OnConnectionMethodDetermined.Broadcast(EConnectionMethod::Direct, TEXT("Direct (UPnP)"));
	}
	else
	{
		UE_LOG(LogNATPunchthrough, Log, TEXT("NATClient: UPnP failed: %s — will fall back to STUN/TURN"), *Result.Error);
	}
}

void UNATClient::HandleMasterServerError(const FString& ErrorMsg)
{
	UE_LOG(LogNATPunchthrough, Error, TEXT("NATClient: Master server error: %s"), *ErrorMsg);
	OnError.Broadcast(ErrorMsg);
}
