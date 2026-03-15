//! Encrypted Keystore Module
//!
//! Provides Ethereum Keystore V3-compatible encrypted key storage for production
//! signer key management. Uses PBKDF2-HMAC-SHA256 key derivation with AES-128-CTR
//! encryption, following the standard Ethereum keystore format compatible with
//! geth, Reth, and other Ethereum clients.
//!
//! # Format
//!
//! Keystore files are JSON documents with the following structure:
//! ```json
//! {
//!   "version": 3,
//!   "id": "uuid-v4",
//!   "address": "hex-address-without-0x",
//!   "crypto": {
//!     "cipher": "aes-128-ctr",
//!     "ciphertext": "hex-encrypted-key",
//!     "cipherparams": { "iv": "hex-initialization-vector" },
//!     "kdf": "pbkdf2",
//!     "kdfparams": { "dklen": 32, "c": 262144, "prf": "hmac-sha256", "salt": "hex-salt" },
//!     "mac": "hex-keccak256-mac"
//!   }
//! }
//! ```

use aes::cipher::{KeyIvInit, StreamCipher};
use alloy_primitives::{keccak256, Address};
use alloy_signer_local::PrivateKeySigner;
use eyre::{bail, ensure, Context, Result};
use serde::{Deserialize, Serialize};
use std::fs;
use std::path::{Path, PathBuf};

use crate::signer::SignerManager;

/// AES-128-CTR cipher type alias
type Aes128Ctr = ctr::Ctr64BE<aes::Aes128>;

/// Default PBKDF2 iteration count (262144 = 2^18, standard for Ethereum keystores)
pub const DEFAULT_PBKDF2_C: u32 = 262_144;

/// Fast PBKDF2 iteration count for testing (still cryptographically functional, just faster)
#[cfg(test)]
const TEST_PBKDF2_C: u32 = 2;

/// Derived key length in bytes
const DKLEN: u32 = 32;

/// Ethereum Keystore V3 format (compatible with geth, Reth, etc.)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KeystoreFile {
    /// Keystore version (always 3)
    pub version: u32,
    /// Account address (hex, without 0x prefix)
    pub address: String,
    /// Encrypted key data
    pub crypto: CryptoJson,
    /// UUID v4 identifier
    pub id: String,
}

/// Encrypted key data following the V3 crypto JSON format
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CryptoJson {
    /// Cipher algorithm (always "aes-128-ctr")
    pub cipher: String,
    /// Hex-encoded encrypted private key
    pub ciphertext: String,
    /// Cipher parameters
    pub cipherparams: CipherParams,
    /// Key derivation function (always "pbkdf2")
    pub kdf: String,
    /// KDF parameters
    pub kdfparams: KdfParams,
    /// Hex-encoded MAC for integrity verification (keccak256)
    pub mac: String,
}

/// AES-128-CTR cipher parameters
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CipherParams {
    /// Hex-encoded 16-byte initialization vector
    pub iv: String,
}

/// PBKDF2 key derivation parameters
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KdfParams {
    /// Derived key length in bytes (always 32)
    pub dklen: u32,
    /// Iteration count (default 262144)
    pub c: u32,
    /// Pseudo-random function (always "hmac-sha256")
    pub prf: String,
    /// Hex-encoded random salt
    pub salt: String,
}

/// Manages encrypted keystores on disk.
///
/// Provides create, import, decrypt, list, and delete operations for
/// Ethereum V3 keystore files.
pub struct KeystoreManager {
    /// Directory where keystore files are stored
    keystore_dir: PathBuf,
    /// PBKDF2 iteration count (configurable for testing)
    pbkdf2_c: u32,
}

impl std::fmt::Debug for KeystoreManager {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("KeystoreManager")
            .field("keystore_dir", &self.keystore_dir)
            .finish()
    }
}

impl KeystoreManager {
    /// Create a new keystore manager with the given directory.
    ///
    /// Uses the standard PBKDF2 iteration count (262144).
    pub fn new(keystore_dir: impl AsRef<Path>) -> Self {
        Self {
            keystore_dir: keystore_dir.as_ref().to_path_buf(),
            pbkdf2_c: DEFAULT_PBKDF2_C,
        }
    }

    /// Create a keystore manager with a custom PBKDF2 iteration count.
    ///
    /// Lower values are faster but less secure. Use only for testing.
    pub fn with_pbkdf2_iterations(keystore_dir: impl AsRef<Path>, pbkdf2_c: u32) -> Self {
        Self {
            keystore_dir: keystore_dir.as_ref().to_path_buf(),
            pbkdf2_c,
        }
    }

    /// Create a new account with a random private key, encrypt and save to disk.
    ///
    /// Returns the address of the newly created account.
    pub fn create_account(&self, password: &str) -> Result<Address> {
        let signer = PrivateKeySigner::random();
        let address = signer.address();
        let key_bytes = signer.credential().to_bytes();
        let key_hex = hex::encode(key_bytes);

        let keystore = encrypt_key_with_iterations(&key_hex, password, self.pbkdf2_c)?;
        self.save_keystore(&address, &keystore)?;

        Ok(address)
    }

    /// Import a private key (hex string), encrypt and save to disk.
    ///
    /// The key can be with or without the `0x` prefix.
    /// Returns the address derived from the key.
    pub fn import_key(&self, private_key_hex: &str, password: &str) -> Result<Address> {
        let clean_hex = private_key_hex
            .strip_prefix("0x")
            .unwrap_or(private_key_hex);

        // Validate the key by parsing it as a signer
        let signer: PrivateKeySigner = clean_hex
            .parse()
            .map_err(|_| eyre::eyre!("Invalid private key format"))?;
        let address = signer.address();

        let keystore = encrypt_key_with_iterations(clean_hex, password, self.pbkdf2_c)?;
        self.save_keystore(&address, &keystore)?;

        Ok(address)
    }

    /// Load and decrypt a keystore file, returning the private key as a hex string.
    ///
    /// Returns an error if the address has no keystore or the password is wrong.
    pub fn decrypt_key(&self, address: &Address, password: &str) -> Result<String> {
        let path = self.find_keystore_path(address)?;
        let data = fs::read_to_string(&path)
            .wrap_err_with(|| format!("Failed to read keystore file: {}", path.display()))?;
        let keystore: KeystoreFile =
            serde_json::from_str(&data).wrap_err("Failed to parse keystore JSON")?;

        decrypt_key(&keystore, password)
    }

    /// List all keystore files (addresses).
    ///
    /// Reads the keystore directory and returns all valid addresses found.
    pub fn list_accounts(&self) -> Result<Vec<Address>> {
        if !self.keystore_dir.exists() {
            return Ok(Vec::new());
        }

        let mut accounts = Vec::new();
        let entries =
            fs::read_dir(&self.keystore_dir).wrap_err("Failed to read keystore directory")?;

        for entry in entries {
            let entry = entry?;
            let path = entry.path();
            if path.extension().is_some_and(|ext| ext == "json") || path.extension().is_none() {
                if let Ok(data) = fs::read_to_string(&path) {
                    if let Ok(keystore) = serde_json::from_str::<KeystoreFile>(&data) {
                        if let Ok(addr) = parse_address(&keystore.address) {
                            accounts.push(addr);
                        }
                    }
                }
            }
        }

        accounts.sort();
        Ok(accounts)
    }

    /// Delete a keystore file for the given address.
    ///
    /// Returns an error if no keystore exists for the address.
    pub fn delete_account(&self, address: &Address) -> Result<()> {
        let path = self.find_keystore_path(address)?;
        fs::remove_file(&path)
            .wrap_err_with(|| format!("Failed to delete keystore: {}", path.display()))?;
        Ok(())
    }

    /// Check if a keystore exists for the given address.
    pub fn has_account(&self, address: &Address) -> bool {
        self.find_keystore_path(address).is_ok()
    }

    /// Load a key from keystore and add it to the signer manager.
    pub fn load_into_signer_manager(
        &self,
        address: &Address,
        password: &str,
        signer_manager: &SignerManager,
    ) -> Result<()> {
        let key_hex = self.decrypt_key(address, password)?;
        signer_manager
            .add_signer_from_hex(&key_hex)
            .map_err(|e| eyre::eyre!("Failed to add signer: {}", e))?;
        Ok(())
    }

    /// Save a keystore file to disk.
    fn save_keystore(&self, address: &Address, keystore: &KeystoreFile) -> Result<()> {
        fs::create_dir_all(&self.keystore_dir).wrap_err("Failed to create keystore directory")?;

        let path = self.keystore_path(address);
        let json =
            serde_json::to_string_pretty(keystore).wrap_err("Failed to serialize keystore")?;
        fs::write(&path, json)
            .wrap_err_with(|| format!("Failed to write keystore: {}", path.display()))?;

        Ok(())
    }

    /// Find the keystore file path for an address (searches directory for matching address).
    fn find_keystore_path(&self, address: &Address) -> Result<PathBuf> {
        if !self.keystore_dir.exists() {
            bail!(
                "Keystore directory does not exist: {}",
                self.keystore_dir.display()
            );
        }

        let addr_hex = hex::encode(address.as_slice()); // 40 lowercase hex chars

        // First check the canonical path
        let canonical = self.keystore_path(address);
        if canonical.exists() {
            return Ok(canonical);
        }

        // Search for any file with a matching address in the filename or content
        let entries =
            fs::read_dir(&self.keystore_dir).wrap_err("Failed to read keystore directory")?;

        for entry in entries {
            let entry = entry?;
            let path = entry.path();
            let filename = path
                .file_name()
                .unwrap_or_default()
                .to_string_lossy()
                .to_lowercase();

            // Check if address appears in filename
            if filename.contains(&addr_hex) {
                return Ok(path);
            }

            // Check file content
            if let Ok(data) = fs::read_to_string(&path) {
                if let Ok(keystore) = serde_json::from_str::<KeystoreFile>(&data) {
                    if keystore.address.to_lowercase() == addr_hex {
                        return Ok(path);
                    }
                }
            }
        }

        bail!("No keystore found for address {}", address)
    }

    /// Get the canonical keystore file path for an address.
    ///
    /// Format: `UTC--{address}.json`
    fn keystore_path(&self, address: &Address) -> PathBuf {
        let addr_hex = hex::encode(address.as_slice()); // 40 lowercase hex chars
        let filename = format!("UTC--{}.json", addr_hex);
        self.keystore_dir.join(filename)
    }
}

/// Encrypt a private key hex string with the given password using the default iteration count.
///
/// Uses PBKDF2-HMAC-SHA256 for key derivation and AES-128-CTR for encryption.
/// The MAC is computed as keccak256(derived_key[16..32] || ciphertext).
pub fn encrypt_key(private_key_hex: &str, password: &str) -> Result<KeystoreFile> {
    encrypt_key_with_iterations(private_key_hex, password, DEFAULT_PBKDF2_C)
}

/// Encrypt a private key hex string with a specified PBKDF2 iteration count.
pub fn encrypt_key_with_iterations(
    private_key_hex: &str,
    password: &str,
    pbkdf2_c: u32,
) -> Result<KeystoreFile> {
    let key_bytes = hex::decode(private_key_hex).wrap_err("Invalid private key hex")?;

    ensure!(
        key_bytes.len() == 32,
        "Private key must be 32 bytes, got {}",
        key_bytes.len()
    );

    // Derive address from the private key
    let signer: PrivateKeySigner = private_key_hex
        .parse()
        .map_err(|_| eyre::eyre!("Invalid private key"))?;
    let address = signer.address();

    // Generate random salt (32 bytes) and IV (16 bytes)
    let salt = random_bytes::<32>();
    let iv = random_bytes::<16>();

    // Derive key using PBKDF2-HMAC-SHA256
    let mut derived_key = [0u8; 32];
    pbkdf2::pbkdf2_hmac::<sha2::Sha256>(password.as_bytes(), &salt, pbkdf2_c, &mut derived_key);

    // Encrypt with AES-128-CTR (use first 16 bytes of derived key as encryption key)
    let mut ciphertext = key_bytes;
    let mut cipher = Aes128Ctr::new(derived_key[..16].into(), iv.as_slice().into());
    cipher.apply_keystream(&mut ciphertext);

    // Compute MAC: keccak256(derived_key[16..32] || ciphertext)
    let mut mac_input = Vec::with_capacity(16 + ciphertext.len());
    mac_input.extend_from_slice(&derived_key[16..32]);
    mac_input.extend_from_slice(&ciphertext);
    let mac = keccak256(&mac_input);

    // Generate UUID
    let id = uuid::Uuid::new_v4().to_string();

    Ok(KeystoreFile {
        version: 3,
        address: hex::encode(address.as_slice()), // 40 hex chars, no 0x prefix
        crypto: CryptoJson {
            cipher: "aes-128-ctr".to_string(),
            ciphertext: hex::encode(&ciphertext),
            cipherparams: CipherParams {
                iv: hex::encode(iv),
            },
            kdf: "pbkdf2".to_string(),
            kdfparams: KdfParams {
                dklen: DKLEN,
                c: pbkdf2_c,
                prf: "hmac-sha256".to_string(),
                salt: hex::encode(salt),
            },
            mac: hex::encode(mac),
        },
        id,
    })
}

/// Decrypt a keystore file with the given password.
///
/// Verifies the MAC before returning the decrypted private key hex.
pub fn decrypt_key(keystore: &KeystoreFile, password: &str) -> Result<String> {
    ensure!(
        keystore.version == 3,
        "Unsupported keystore version: {}",
        keystore.version
    );
    ensure!(
        keystore.crypto.cipher == "aes-128-ctr",
        "Unsupported cipher: {}",
        keystore.crypto.cipher
    );
    ensure!(
        keystore.crypto.kdf == "pbkdf2",
        "Unsupported KDF: {} (only pbkdf2 is supported)",
        keystore.crypto.kdf
    );

    let salt = hex::decode(&keystore.crypto.kdfparams.salt).wrap_err("Invalid salt hex")?;
    let iv = hex::decode(&keystore.crypto.cipherparams.iv).wrap_err("Invalid IV hex")?;
    let ciphertext = hex::decode(&keystore.crypto.ciphertext).wrap_err("Invalid ciphertext hex")?;
    let expected_mac = hex::decode(&keystore.crypto.mac).wrap_err("Invalid MAC hex")?;

    ensure!(iv.len() == 16, "IV must be 16 bytes, got {}", iv.len());
    ensure!(
        ciphertext.len() == 32,
        "Ciphertext must be 32 bytes, got {}",
        ciphertext.len()
    );

    // Derive key using PBKDF2-HMAC-SHA256
    let dklen = keystore.crypto.kdfparams.dklen as usize;
    let mut derived_key = vec![0u8; dklen];
    pbkdf2::pbkdf2_hmac::<sha2::Sha256>(
        password.as_bytes(),
        &salt,
        keystore.crypto.kdfparams.c,
        &mut derived_key,
    );

    // Verify MAC: keccak256(derived_key[16..32] || ciphertext)
    let mut mac_input = Vec::with_capacity(16 + ciphertext.len());
    mac_input.extend_from_slice(&derived_key[16..32]);
    mac_input.extend_from_slice(&ciphertext);
    let computed_mac = keccak256(&mac_input);

    ensure!(
        computed_mac.as_slice() == expected_mac.as_slice(),
        "MAC verification failed: wrong password or corrupted keystore"
    );

    // Decrypt with AES-128-CTR
    let mut plaintext = ciphertext;
    let mut cipher = Aes128Ctr::new(derived_key[..16].into(), iv.as_slice().into());
    cipher.apply_keystream(&mut plaintext);

    Ok(hex::encode(&plaintext))
}

/// Generate N random bytes using alloy_primitives::B256::random() as entropy source.
fn random_bytes<const N: usize>() -> [u8; N] {
    let mut result = [0u8; N];
    // Use B256::random() which uses the platform's CSPRNG (getrandom)
    let mut filled = 0;
    while filled < N {
        let random = alloy_primitives::B256::random();
        let remaining = N - filled;
        let copy_len = remaining.min(32);
        result[filled..filled + copy_len].copy_from_slice(&random[..copy_len]);
        filled += copy_len;
    }
    result
}

/// Parse an address string (with or without 0x prefix).
fn parse_address(addr_str: &str) -> Result<Address> {
    let with_prefix = if addr_str.starts_with("0x") || addr_str.starts_with("0X") {
        addr_str.to_string()
    } else {
        format!("0x{}", addr_str)
    };
    with_prefix
        .parse::<Address>()
        .map_err(|e| eyre::eyre!("Invalid address '{}': {}", addr_str, e))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;

    /// Helper to create a temporary keystore manager (uses fast PBKDF2 for tests)
    fn temp_keystore() -> (KeystoreManager, TempDir) {
        let dir = TempDir::new().expect("Failed to create temp dir");
        let manager =
            KeystoreManager::with_pbkdf2_iterations(dir.path().join("keystore"), TEST_PBKDF2_C);
        (manager, dir)
    }

    /// Dev key for deterministic tests
    const TEST_KEY: &str = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80";
    const TEST_PASSWORD: &str = "test-password-123";

    // -------------------------------------------------------------------------
    // Test 1: create_account + decrypt round-trip
    // -------------------------------------------------------------------------
    #[test]
    fn test_create_account_and_decrypt_roundtrip() {
        let (manager, _dir) = temp_keystore();

        let address = manager.create_account(TEST_PASSWORD).unwrap();

        // Decrypt should succeed and produce a valid 32-byte hex key
        let key_hex = manager.decrypt_key(&address, TEST_PASSWORD).unwrap();
        assert_eq!(
            key_hex.len(),
            64,
            "Decrypted key should be 64 hex chars (32 bytes)"
        );

        // The decrypted key should parse as a valid signer with the same address
        let signer: PrivateKeySigner = key_hex.parse().unwrap();
        assert_eq!(signer.address(), address);
    }

    // -------------------------------------------------------------------------
    // Test 2: import_key + decrypt round-trip
    // -------------------------------------------------------------------------
    #[test]
    fn test_import_key_and_decrypt_roundtrip() {
        let (manager, _dir) = temp_keystore();

        let address = manager.import_key(TEST_KEY, TEST_PASSWORD).unwrap();

        let decrypted = manager.decrypt_key(&address, TEST_PASSWORD).unwrap();
        assert_eq!(decrypted, TEST_KEY);
    }

    // -------------------------------------------------------------------------
    // Test 3: wrong password fails
    // -------------------------------------------------------------------------
    #[test]
    fn test_wrong_password_fails() {
        let (manager, _dir) = temp_keystore();

        let address = manager.import_key(TEST_KEY, TEST_PASSWORD).unwrap();

        let result = manager.decrypt_key(&address, "wrong-password");
        assert!(result.is_err());
        let err_msg = result.unwrap_err().to_string();
        assert!(
            err_msg.contains("MAC verification failed"),
            "Expected MAC verification error, got: {}",
            err_msg
        );
    }

    // -------------------------------------------------------------------------
    // Test 4: list_accounts
    // -------------------------------------------------------------------------
    #[test]
    fn test_list_accounts() {
        let (manager, _dir) = temp_keystore();

        assert!(manager.list_accounts().unwrap().is_empty());

        let addr1 = manager.import_key(TEST_KEY, TEST_PASSWORD).unwrap();

        let key2 = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d";
        let addr2 = manager.import_key(key2, TEST_PASSWORD).unwrap();

        let accounts = manager.list_accounts().unwrap();
        assert_eq!(accounts.len(), 2);
        assert!(accounts.contains(&addr1));
        assert!(accounts.contains(&addr2));
    }

    // -------------------------------------------------------------------------
    // Test 5: delete_account
    // -------------------------------------------------------------------------
    #[test]
    fn test_delete_account() {
        let (manager, _dir) = temp_keystore();

        let address = manager.import_key(TEST_KEY, TEST_PASSWORD).unwrap();
        assert!(manager.has_account(&address));

        manager.delete_account(&address).unwrap();
        assert!(!manager.has_account(&address));

        // Decrypt should fail after deletion
        assert!(manager.decrypt_key(&address, TEST_PASSWORD).is_err());
    }

    // -------------------------------------------------------------------------
    // Test 6: has_account
    // -------------------------------------------------------------------------
    #[test]
    fn test_has_account() {
        let (manager, _dir) = temp_keystore();
        let fake_addr: Address = "0x0000000000000000000000000000000000000099"
            .parse()
            .unwrap();

        assert!(!manager.has_account(&fake_addr));

        let address = manager.import_key(TEST_KEY, TEST_PASSWORD).unwrap();
        assert!(manager.has_account(&address));
        assert!(!manager.has_account(&fake_addr));
    }

    // -------------------------------------------------------------------------
    // Test 7: keystore file format (JSON structure)
    // -------------------------------------------------------------------------
    #[test]
    fn test_keystore_file_format() {
        let keystore = encrypt_key_with_iterations(TEST_KEY, TEST_PASSWORD, TEST_PBKDF2_C).unwrap();

        assert_eq!(keystore.version, 3);
        assert_eq!(keystore.crypto.cipher, "aes-128-ctr");
        assert_eq!(keystore.crypto.kdf, "pbkdf2");
        assert_eq!(keystore.crypto.kdfparams.dklen, 32);
        assert_eq!(keystore.crypto.kdfparams.c, TEST_PBKDF2_C);
        assert_eq!(keystore.crypto.kdfparams.prf, "hmac-sha256");

        // UUID should be valid v4
        let uuid = uuid::Uuid::parse_str(&keystore.id).unwrap();
        assert_eq!(uuid.get_version_num(), 4);

        // Ciphertext should be 32 bytes (64 hex chars)
        assert_eq!(keystore.crypto.ciphertext.len(), 64);
        // IV should be 16 bytes (32 hex chars)
        assert_eq!(keystore.crypto.cipherparams.iv.len(), 32);
        // Salt should be 32 bytes (64 hex chars)
        assert_eq!(keystore.crypto.kdfparams.salt.len(), 64);
        // MAC should be 32 bytes (64 hex chars)
        assert_eq!(keystore.crypto.mac.len(), 64);

        // Should serialize to valid JSON
        let json = serde_json::to_string_pretty(&keystore).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed["version"], 3);
        assert_eq!(parsed["crypto"]["cipher"], "aes-128-ctr");
    }

    // -------------------------------------------------------------------------
    // Test 8: address derivation from key
    // -------------------------------------------------------------------------
    #[test]
    fn test_address_derivation_from_key() {
        let (manager, _dir) = temp_keystore();

        // Known dev key -> known address
        let expected_signer: PrivateKeySigner = TEST_KEY.parse().unwrap();
        let expected_addr = expected_signer.address();

        let address = manager.import_key(TEST_KEY, TEST_PASSWORD).unwrap();
        assert_eq!(address, expected_addr);

        // Address in keystore file should match
        let keystore = encrypt_key_with_iterations(TEST_KEY, TEST_PASSWORD, TEST_PBKDF2_C).unwrap();
        let stored_addr = parse_address(&keystore.address).unwrap();
        assert_eq!(stored_addr, expected_addr);
    }

    // -------------------------------------------------------------------------
    // Test 9: MAC verification
    // -------------------------------------------------------------------------
    #[test]
    fn test_mac_verification() {
        let keystore = encrypt_key_with_iterations(TEST_KEY, TEST_PASSWORD, TEST_PBKDF2_C).unwrap();

        // Correct password should succeed
        let result = decrypt_key(&keystore, TEST_PASSWORD);
        assert!(result.is_ok());

        // Tampered ciphertext should fail MAC
        let mut tampered = keystore.clone();
        let mut ct_bytes = hex::decode(&tampered.crypto.ciphertext).unwrap();
        ct_bytes[0] ^= 0xFF; // flip bits
        tampered.crypto.ciphertext = hex::encode(&ct_bytes);
        let result = decrypt_key(&tampered, TEST_PASSWORD);
        assert!(result.is_err());
        assert!(result
            .unwrap_err()
            .to_string()
            .contains("MAC verification failed"));

        // Tampered MAC should fail
        let mut tampered_mac = keystore.clone();
        tampered_mac.crypto.mac = "00".repeat(32);
        let result = decrypt_key(&tampered_mac, TEST_PASSWORD);
        assert!(result.is_err());
    }

    // -------------------------------------------------------------------------
    // Test 10: empty keystore directory
    // -------------------------------------------------------------------------
    #[test]
    fn test_empty_keystore_directory() {
        let (manager, _dir) = temp_keystore();

        // list_accounts on non-existent dir returns empty
        let accounts = manager.list_accounts().unwrap();
        assert!(accounts.is_empty());

        // has_account returns false
        let addr: Address = "0x0000000000000000000000000000000000000001"
            .parse()
            .unwrap();
        assert!(!manager.has_account(&addr));
    }

    // -------------------------------------------------------------------------
    // Test 11: duplicate import (overwrite)
    // -------------------------------------------------------------------------
    #[test]
    fn test_duplicate_import() {
        let (manager, _dir) = temp_keystore();

        let addr1 = manager.import_key(TEST_KEY, "password1").unwrap();
        let addr2 = manager.import_key(TEST_KEY, "password2").unwrap();

        // Same key should produce same address
        assert_eq!(addr1, addr2);

        // Should be decryptable with the latest password
        let decrypted = manager.decrypt_key(&addr2, "password2").unwrap();
        assert_eq!(decrypted, TEST_KEY);

        // Old password should fail (file was overwritten)
        assert!(manager.decrypt_key(&addr1, "password1").is_err());
    }

    // -------------------------------------------------------------------------
    // Test 12: invalid key format
    // -------------------------------------------------------------------------
    #[test]
    fn test_invalid_key_format() {
        let (manager, _dir) = temp_keystore();

        // Not hex
        assert!(manager
            .import_key("not-a-valid-hex-key", TEST_PASSWORD)
            .is_err());

        // Too short
        assert!(manager.import_key("abcd", TEST_PASSWORD).is_err());

        // Too long
        assert!(manager.import_key(&"ab".repeat(33), TEST_PASSWORD).is_err());

        // Empty
        assert!(manager.import_key("", TEST_PASSWORD).is_err());
    }

    // -------------------------------------------------------------------------
    // Test 13: keystore persistence across instances
    // -------------------------------------------------------------------------
    #[test]
    fn test_keystore_persistence_across_instances() {
        let dir = TempDir::new().unwrap();
        let keystore_path = dir.path().join("keystore");

        let address;
        {
            let manager1 = KeystoreManager::with_pbkdf2_iterations(&keystore_path, TEST_PBKDF2_C);
            address = manager1.import_key(TEST_KEY, TEST_PASSWORD).unwrap();
            assert!(manager1.has_account(&address));
        }

        // Create a new manager instance pointing to the same directory
        {
            let manager2 = KeystoreManager::with_pbkdf2_iterations(&keystore_path, TEST_PBKDF2_C);
            assert!(manager2.has_account(&address));

            let decrypted = manager2.decrypt_key(&address, TEST_PASSWORD).unwrap();
            assert_eq!(decrypted, TEST_KEY);

            let accounts = manager2.list_accounts().unwrap();
            assert_eq!(accounts.len(), 1);
            assert!(accounts.contains(&address));
        }
    }

    // -------------------------------------------------------------------------
    // Test 14: load_into_signer_manager
    // -------------------------------------------------------------------------
    #[tokio::test]
    async fn test_load_into_signer_manager() {
        let (manager, _dir) = temp_keystore();

        let address = manager.import_key(TEST_KEY, TEST_PASSWORD).unwrap();

        let signer_manager = SignerManager::new();
        assert!(!signer_manager.has_signer(&address));

        manager
            .load_into_signer_manager(&address, TEST_PASSWORD, &signer_manager)
            .unwrap();

        assert!(signer_manager.has_signer(&address));

        // Verify the loaded signer can sign
        let hash = alloy_primitives::B256::ZERO;
        let sig = signer_manager.sign_hash(&address, hash).await;
        assert!(sig.is_ok());
    }

    // -------------------------------------------------------------------------
    // Test 15: concurrent access
    // -------------------------------------------------------------------------
    #[tokio::test]
    async fn test_concurrent_access() {
        let dir = TempDir::new().unwrap();
        let keystore_path = dir.path().join("keystore");

        // Import a key first
        let manager = KeystoreManager::with_pbkdf2_iterations(&keystore_path, TEST_PBKDF2_C);
        let address = manager.import_key(TEST_KEY, TEST_PASSWORD).unwrap();

        // Spawn multiple concurrent decrypt operations
        let keystore_path = Arc::new(keystore_path);
        let mut handles = vec![];

        for _ in 0..5 {
            let path = keystore_path.clone();
            let addr = address;
            handles.push(tokio::spawn(async move {
                let mgr = KeystoreManager::with_pbkdf2_iterations(path.as_ref(), TEST_PBKDF2_C);
                mgr.decrypt_key(&addr, TEST_PASSWORD).unwrap()
            }));
        }

        let mut results = Vec::new();
        for handle in handles {
            results.push(handle.await.unwrap());
        }

        assert_eq!(results.len(), 5);
        for key in &results {
            assert_eq!(key, TEST_KEY);
        }
    }

    // -------------------------------------------------------------------------
    // Test 16: import key with 0x prefix
    // -------------------------------------------------------------------------
    #[test]
    fn test_import_key_with_0x_prefix() {
        let (manager, _dir) = temp_keystore();

        let key_with_prefix = format!("0x{}", TEST_KEY);
        let address = manager.import_key(&key_with_prefix, TEST_PASSWORD).unwrap();

        let decrypted = manager.decrypt_key(&address, TEST_PASSWORD).unwrap();
        assert_eq!(decrypted, TEST_KEY);
    }

    // -------------------------------------------------------------------------
    // Test 17: delete nonexistent account fails
    // -------------------------------------------------------------------------
    #[test]
    fn test_delete_nonexistent_account() {
        let (manager, _dir) = temp_keystore();

        // Create the directory so the "directory does not exist" check doesn't fire
        fs::create_dir_all(&manager.keystore_dir).ok();

        let fake_addr: Address = "0x0000000000000000000000000000000000000099"
            .parse()
            .unwrap();
        let result = manager.delete_account(&fake_addr);
        assert!(result.is_err());
    }

    // -------------------------------------------------------------------------
    // Test 18: JSON serialization round-trip
    // -------------------------------------------------------------------------
    #[test]
    fn test_json_serialization_roundtrip() {
        let keystore = encrypt_key_with_iterations(TEST_KEY, TEST_PASSWORD, TEST_PBKDF2_C).unwrap();

        let json = serde_json::to_string_pretty(&keystore).unwrap();
        let deserialized: KeystoreFile = serde_json::from_str(&json).unwrap();

        assert_eq!(deserialized.version, keystore.version);
        assert_eq!(deserialized.address, keystore.address);
        assert_eq!(deserialized.crypto.cipher, keystore.crypto.cipher);
        assert_eq!(deserialized.crypto.ciphertext, keystore.crypto.ciphertext);
        assert_eq!(deserialized.crypto.mac, keystore.crypto.mac);
        assert_eq!(deserialized.id, keystore.id);

        // Should still decrypt correctly after round-trip
        let decrypted = decrypt_key(&deserialized, TEST_PASSWORD).unwrap();
        assert_eq!(decrypted, TEST_KEY);
    }

    // -------------------------------------------------------------------------
    // Test 19: multiple accounts lifecycle
    // -------------------------------------------------------------------------
    #[test]
    fn test_multiple_accounts_lifecycle() {
        let (manager, _dir) = temp_keystore();

        let keys = &[
            "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
            "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d",
            "5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a",
        ];

        let mut addresses = Vec::new();
        for key in keys {
            let addr = manager.import_key(key, TEST_PASSWORD).unwrap();
            addresses.push(addr);
        }

        assert_eq!(manager.list_accounts().unwrap().len(), 3);

        // Delete the middle one
        manager.delete_account(&addresses[1]).unwrap();
        assert_eq!(manager.list_accounts().unwrap().len(), 2);
        assert!(!manager.has_account(&addresses[1]));
        assert!(manager.has_account(&addresses[0]));
        assert!(manager.has_account(&addresses[2]));

        // Decrypt remaining keys
        let d0 = manager.decrypt_key(&addresses[0], TEST_PASSWORD).unwrap();
        assert_eq!(d0, keys[0]);
        let d2 = manager.decrypt_key(&addresses[2], TEST_PASSWORD).unwrap();
        assert_eq!(d2, keys[2]);
    }

    // -------------------------------------------------------------------------
    // Test 20: encrypt_key and decrypt_key standalone functions
    // -------------------------------------------------------------------------
    #[test]
    fn test_encrypt_decrypt_standalone() {
        let encrypted =
            encrypt_key_with_iterations(TEST_KEY, "my-password", TEST_PBKDF2_C).unwrap();
        let decrypted = decrypt_key(&encrypted, "my-password").unwrap();
        assert_eq!(decrypted, TEST_KEY);
    }

    // -------------------------------------------------------------------------
    // Helper: TempDir using std (no external tempfile crate needed)
    // -------------------------------------------------------------------------
    struct TempDir {
        path: PathBuf,
    }

    impl TempDir {
        fn new() -> Result<Self, std::io::Error> {
            let mut path = std::env::temp_dir();
            let id = alloy_primitives::B256::random();
            path.push(format!("meowchain-keystore-test-{}", hex::encode(&id[..8])));
            fs::create_dir_all(&path)?;
            Ok(Self { path })
        }

        fn path(&self) -> &Path {
            &self.path
        }
    }

    impl Drop for TempDir {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.path);
        }
    }
}
