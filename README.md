# Go Email Agent

## Overview
The Go Email Agent is a simple yet powerful email communication tool built using Go (Golang). This project is designed to send and receive emails with ease, harnessing the capabilities of the Go programming language for efficiency and effectiveness.

## Features
- Send emails using SMTP
- Receive emails via IMAP
- Support for HTML and plain text emails
- Attachment support
- Configurable settings for different email providers
- Secure connections using SSL/TLS

## Getting Started
### Prerequisites
- Go installed on your machine (version 1.15 or later)
- An email account with SMTP and IMAP access (e.g., Gmail, Outlook)

### Installation
1. Clone this repository:
   ```bash
   git clone https://github.com/Jaidheep05/go-email-agent.git
   cd go-email-agent
   ```
2. Run the following command to install dependencies:
   ```bash
   go mod tidy
   ```

## Configuration
Before running the email agent, you need to configure your email settings. Create a `config.yaml` file as follows:
```yaml
smtp:
  host: smtp.example.com
  port: 587
  username: your_email@example.com
  password: your_password

imap:
  host: imap.example.com
  port: 993
  username: your_email@example.com
  password: your_password
```

## Usage
To send an email:
```bash
go run main.go send -to recipient@example.com -subject "Subject Here" -body "Email body content"
```
To receive emails:
```bash
go run main.go receive
```

## Contributing
Contributions are welcome! Please fork the repo and submit a pull request.

## License
This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

## Acknowledgments
- Inspired by the need for a simple email client.
- Thanks to the Go community for their support and inspiration!