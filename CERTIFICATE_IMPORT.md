# Certificate Import Instructions

When you start the Transparent Proxy, it automatically generates a self-signed CA certificate in two formats:

- `transparent-ca.crt` - PEM format (for Firefox, Linux, macOS)
- `transparent-ca.p12` - PKCS12 format (for Chrome, Windows)

**Password for `.p12` file: `changeit`**

## Import Instructions

### Chrome (Windows/macOS/Linux)

1. Open Chrome Settings
2. Search for "certificates" or navigate to: `chrome://settings/security`
3. Click "Manage certificates" or "Manage device certificates"
4. Click "Import" 
5. Select `transparent-ca.p12`
6. Enter password: **`changeit`**
7. Select "Trusted Root Certification Authorities" as the certificate store
8. Complete the import

### Firefox

1. Open Firefox Settings
2. Search for "certificates" or navigate to: `about:preferences#privacy`
3. Scroll to "Certificates" section
4. Click "View Certificates"
5. Go to "Authorities" tab
6. Click "Import"
7. Select `transparent-ca.crt`
8. Check "Trust this CA to identify websites"
9. Click OK

### macOS System (Safari, curl, etc.)

```bash
# Open Keychain Access and import the certificate
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain transparent-ca.crt
```

Or use the GUI:
1. Double-click `transparent-ca.crt`
2. In Keychain Access, find "Transparent Proxy CA"
3. Double-click it, expand "Trust"
4. Set "When using this certificate" to "Always Trust"
5. Close and enter your password

### Linux (Ubuntu/Debian)

```bash
# Copy to system certificate directory
sudo cp transparent-ca.crt /usr/local/share/ca-certificates/transparent-ca.crt

# Update certificate store
sudo update-ca-certificates
```

### Linux (Fedora/RHEL/CentOS)

```bash
# Copy to system certificate directory
sudo cp transparent-ca.crt /etc/pki/ca-trust/source/anchors/

# Update certificate store
sudo update-ca-trust
```

## Important Notes

- The certificate is regenerated each time the proxy starts
- You'll need to re-import if you restart the proxy
- The `.p12` file password is: **`changeit`**
- For production use, consider generating a persistent certificate and storing it securely

## Verifying Import

After importing, you can verify the certificate is trusted:

### Chrome
Visit a site through the proxy - you should see a padlock icon instead of a warning

### Firefox  
Visit a site through the proxy - you should see a padlock icon instead of a warning

### Command line (Linux/macOS)
```bash
# Test with curl
curl -x http://localhost:8080 https://www.google.com
# Should work without SSL errors
```

## Troubleshooting

**Chrome still shows warnings:**
- Make sure you imported the `.p12` file (not the `.crt`)
- Ensure you entered the password: `changeit`
- Ensure you selected "Trusted Root Certification Authorities"
- Try restarting Chrome after import

**Chrome says "Invalid or corrupt file":**
- Make sure you're using the `.p12` file
- Ensure the file was generated correctly (check file size > 0)
- Try regenerating by restarting the proxy server

**Firefox still shows warnings:**
- Make sure you checked "Trust this CA to identify websites"
- Try restarting Firefox after import

**The certificate files don't exist:**
- Start the proxy server - certificates are generated on startup
- Check the console output for any error messages
