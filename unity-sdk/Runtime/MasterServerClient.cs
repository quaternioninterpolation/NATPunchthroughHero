using System;
using System.Collections.Generic;
using System.Text;
using System.Threading.Tasks;
using UnityEngine;
using UnityEngine.Networking;

namespace NatPunchthrough
{
    /// <summary>
    /// REST client for the NAT Punchthrough Hero master server.
    /// Handles game registration, listing, heartbeats, and TURN credentials.
    /// </summary>
    public class MasterServerClient
    {
        private readonly string _baseUrl;
        private readonly string _apiKey;

        /// <summary>
        /// Create a new master server client.
        /// </summary>
        /// <param name="baseUrl">Server URL (e.g., "http://localhost:8080")</param>
        /// <param name="apiKey">API key for authentication (empty = no auth)</param>
        public MasterServerClient(string baseUrl, string apiKey = "")
        {
            _baseUrl = baseUrl.TrimEnd('/');
            _apiKey = apiKey;
        }

        #region Game Management

        /// <summary>
        /// Register a new game on the master server.
        /// </summary>
        public async Task<RegisterResult> RegisterGame(GameRegistration info)
        {
            string json = JsonUtility.ToJson(info);
            string response = await PostJson($"{_baseUrl}/api/games", json);

            if (string.IsNullOrEmpty(response))
            {
                return new RegisterResult { Success = false, Error = "Request failed" };
            }

            try
            {
                var result = JsonUtility.FromJson<RegisterResult>(response);
                result.Success = !string.IsNullOrEmpty(result.GameId);
                return result;
            }
            catch (Exception e)
            {
                return new RegisterResult { Success = false, Error = e.Message };
            }
        }

        /// <summary>
        /// List available games. Optionally filter by join code.
        /// </summary>
        public async Task<List<GameInfo>> ListGames(string code = null)
        {
            string url = $"{_baseUrl}/api/games";
            if (!string.IsNullOrEmpty(code))
            {
                url += $"?code={UnityWebRequest.EscapeURL(code)}";
            }

            string response = await Get(url);

            if (string.IsNullOrEmpty(response))
            {
                return new List<GameInfo>();
            }

            try
            {
                // Unity's JsonUtility doesn't handle arrays at root level.
                // Wrap in object for parsing.
                string wrapped = "{\"items\":" + response + "}";
                var wrapper = JsonUtility.FromJson<GameListWrapper>(wrapped);
                return wrapper?.items ?? new List<GameInfo>();
            }
            catch (Exception e)
            {
                Debug.LogWarning($"[MasterServerClient] Failed to parse game list: {e.Message}");
                return new List<GameInfo>();
            }
        }

        /// <summary>
        /// Get details of a specific game.
        /// </summary>
        public async Task<GameInfo> GetGame(string gameId)
        {
            string response = await Get($"{_baseUrl}/api/games/{gameId}");
            if (string.IsNullOrEmpty(response)) return null;

            try
            {
                return JsonUtility.FromJson<GameInfo>(response);
            }
            catch
            {
                return null;
            }
        }

        /// <summary>
        /// Send a heartbeat to keep the game alive.
        /// Call this every 30 seconds while hosting.
        /// </summary>
        public async Task<bool> SendHeartbeat(string gameId, string hostToken)
        {
            string url = $"{_baseUrl}/api/games/{gameId}/heartbeat";
            string response = await PostWithAuth(url, "", hostToken);
            return !string.IsNullOrEmpty(response);
        }

        /// <summary>
        /// Remove a game from the master server.
        /// </summary>
        public async Task<bool> DeregisterGame(string gameId, string hostToken)
        {
            string url = $"{_baseUrl}/api/games/{gameId}";
            string response = await DeleteWithAuth(url, hostToken);
            return !string.IsNullOrEmpty(response);
        }

        #endregion

        #region TURN Credentials

        /// <summary>
        /// Get time-limited TURN relay credentials for a game session.
        /// </summary>
        public async Task<TurnCredentials> GetTurnCredentials(string gameId)
        {
            string url = $"{_baseUrl}/api/games/{gameId}/turn";
            string response = await Get(url);

            if (string.IsNullOrEmpty(response)) return null;

            try
            {
                return JsonUtility.FromJson<TurnCredentials>(response);
            }
            catch (Exception e)
            {
                Debug.LogWarning($"[MasterServerClient] Failed to parse TURN credentials: {e.Message}");
                return null;
            }
        }

        #endregion

        #region Health

        /// <summary>
        /// Check if the master server is healthy.
        /// </summary>
        public async Task<bool> CheckHealth()
        {
            string response = await Get($"{_baseUrl}/api/health");
            return !string.IsNullOrEmpty(response);
        }

        #endregion

        #region HTTP Helpers

        private async Task<string> Get(string url)
        {
            using var request = UnityWebRequest.Get(url);
            AddHeaders(request);

            var operation = request.SendWebRequest();

            while (!operation.isDone)
                await Task.Yield();

            if (request.result != UnityWebRequest.Result.Success)
            {
                Debug.LogWarning($"[MasterServerClient] GET {url} failed: {request.error}");
                return null;
            }

            return request.downloadHandler.text;
        }

        private async Task<string> PostJson(string url, string json)
        {
            using var request = new UnityWebRequest(url, "POST");
            byte[] bodyRaw = Encoding.UTF8.GetBytes(json);
            request.uploadHandler = new UploadHandlerRaw(bodyRaw);
            request.downloadHandler = new DownloadHandlerBuffer();
            request.SetRequestHeader("Content-Type", "application/json");
            AddHeaders(request);

            var operation = request.SendWebRequest();

            while (!operation.isDone)
                await Task.Yield();

            if (request.result != UnityWebRequest.Result.Success)
            {
                Debug.LogWarning($"[MasterServerClient] POST {url} failed: {request.error}");
                return null;
            }

            return request.downloadHandler.text;
        }

        private async Task<string> PostWithAuth(string url, string body, string token)
        {
            using var request = new UnityWebRequest(url, "POST");
            if (!string.IsNullOrEmpty(body))
            {
                byte[] bodyRaw = Encoding.UTF8.GetBytes(body);
                request.uploadHandler = new UploadHandlerRaw(bodyRaw);
            }
            request.downloadHandler = new DownloadHandlerBuffer();
            request.SetRequestHeader("Authorization", $"Bearer {token}");
            AddHeaders(request);

            var operation = request.SendWebRequest();

            while (!operation.isDone)
                await Task.Yield();

            if (request.result != UnityWebRequest.Result.Success)
            {
                Debug.LogWarning($"[MasterServerClient] POST {url} failed: {request.error}");
                return null;
            }

            return request.downloadHandler.text;
        }

        private async Task<string> DeleteWithAuth(string url, string token)
        {
            using var request = UnityWebRequest.Delete(url);
            request.downloadHandler = new DownloadHandlerBuffer();
            request.SetRequestHeader("Authorization", $"Bearer {token}");
            AddHeaders(request);

            var operation = request.SendWebRequest();

            while (!operation.isDone)
                await Task.Yield();

            if (request.result != UnityWebRequest.Result.Success)
            {
                Debug.LogWarning($"[MasterServerClient] DELETE {url} failed: {request.error}");
                return null;
            }

            return request.downloadHandler.text;
        }

        private void AddHeaders(UnityWebRequest request)
        {
            if (!string.IsNullOrEmpty(_apiKey))
            {
                request.SetRequestHeader("X-API-Key", _apiKey);
            }
        }

        #endregion
    }

    #region Data Types

    [Serializable]
    public class GameRegistration
    {
        public string name;
        public int max_players = 4;
        public int current_players = 1;
        public string nat_type = "unknown";
        // Note: Unity's JsonUtility doesn't support Dictionary.
        // Use a custom serializable class or JSON library for metadata.
    }

    [Serializable]
    public class RegisterResult
    {
        [NonSerialized] public bool Success;

        public string id;
        public string join_code;
        public string host_token;
        [NonSerialized] public string Error;

        // Convenience properties
        public string GameId => id;
        public string JoinCode => join_code;
        public string HostToken => host_token;
    }

    [Serializable]
    public class GameInfo
    {
        public string id;
        public string name;
        public string join_code;
        public int max_players;
        public int current_players;
        public string nat_type;
        public string created_at;

        // Convenience properties
        public string Id => id;
        public string Name => name;
        public string JoinCode => join_code;
        public int MaxPlayers => max_players;
        public int CurrentPlayers => current_players;
        public string NatType => nat_type;
    }

    [Serializable]
    public class TurnCredentials
    {
        public string username;
        public string password;
        public int ttl;
        public string[] uris;

        public string Username => username;
        public string Password => password;
        public int TTL => ttl;
        public string[] URIs => uris;
    }

    [Serializable]
    internal class GameListWrapper
    {
        public List<GameInfo> items;
    }

    #endregion
}
