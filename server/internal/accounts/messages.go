package accounts

import (
	"fmt"

	hdmail "github.com/BertVanBaelen/HomeDrive/server/internal/mail"
)

// codeMessage is the email with a sign-in code, in the user's language.
func codeMessage(to, locale, code string) hdmail.Message {
	pretty := code[:4] + " " + code[4:]
	if locale == "nl" {
		return hdmail.Message{
			To:      to,
			Subject: fmt.Sprintf("Je Homedrive-code: %s", pretty),
			Body: fmt.Sprintf("Je code om aan te melden bij Homedrive is:\n\n    %s\n\n"+
				"De code is 10 minuten geldig.\n\n"+
				"Heb je niet geprobeerd aan te melden? Dan kent iemand je wachtwoord. "+
				"Vraag je beheerder om het te wijzigen.\n", pretty),
		}
	}
	return hdmail.Message{
		To:      to,
		Subject: fmt.Sprintf("Your Homedrive code: %s", pretty),
		Body: fmt.Sprintf("Your code to sign in to Homedrive is:\n\n    %s\n\n"+
			"It is valid for 10 minutes.\n\n"+
			"Didn't try to sign in? Then someone knows your password. "+
			"Ask your administrator to change it.\n", pretty),
	}
}
