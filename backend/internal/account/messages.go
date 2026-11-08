package account

import (
	"fmt"

	mailer "github.com/jlindbergh/track-a-doodle/backend/internal/mail"
)

func verifyMessage(to, link string) mailer.Message {
	return mailer.Message{
		To:      to,
		Subject: "Confirm your Track-a-doodle account",
		Body: fmt.Sprintf(`Hi!

Someone, hopefully you, signed up for Track-a-doodle with this email address.

To confirm the account, open this link and enter the password you chose:

%s

The link works for %d hours. If you didn't sign up, ignore this email:
the account is never activated and is deleted after a few days.
`, link, int(VerifyTokenTTL.Hours())),
	}
}

func existsMessage(to, appURL string) mailer.Message {
	return mailer.Message{
		To:      to,
		Subject: "You already have a Track-a-doodle account",
		Body: fmt.Sprintf(`Hi!

Someone tried to sign up for Track-a-doodle with this email address, but
there is already an account for it. Nothing has been changed.

If it was you, sign in here instead:

%s

If it wasn't you, you can ignore this email.
`, appURL),
	}
}
